// Package api implements the Patroni REST API server.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/internal/ha"
	"github.com/patroni/patroni-go/internal/postgresql"
	"github.com/patroni/patroni-go/pkg/types"
)

// Server implements the REST API server.
type Server struct {
	config *config.Config
	ha     *ha.HA
	pg     *postgresql.Postgresql
	server *http.Server
	router chi.Router
}

// New creates a new API server.
func New(cfg *config.Config, haInstance *ha.HA, pg *postgresql.Postgresql) *Server {
	s := &Server{
		config: cfg,
		ha:     haInstance,
		pg:     pg,
	}

	s.setupRoutes()
	return s
}

// setupRoutes configures the API routes.
func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Health check endpoints
	r.Get("/", s.handleHealth)
	r.Get("/health", s.handleHealth)
	r.Get("/primary", s.handlePrimary)
	r.Get("/master", s.handlePrimary) // Alias
	r.Get("/replica", s.handleReplica)
	r.Get("/standby", s.handleReplica) // Alias
	r.Get("/read-write", s.handlePrimary)
	r.Get("/read-only", s.handleReadOnly)
	r.Get("/leader", s.handleLeader)
	r.Get("/standby-leader", s.handleStandbyLeader)
	r.Get("/synchronous", s.handleSynchronous)
	r.Get("/sync", s.handleSynchronous) // Alias
	r.Get("/asynchronous", s.handleAsynchronous)
	r.Get("/async", s.handleAsynchronous) // Alias

	// Status endpoints
	r.Get("/patroni", s.handlePatroni)
	r.Get("/cluster", s.handleCluster)
	r.Get("/history", s.handleHistory)
	r.Get("/config", s.handleGetConfig)
	r.Get("/liveness", s.handleLiveness)
	r.Get("/readiness", s.handleReadiness)

	// Management endpoints (with authentication)
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)

		r.Post("/restart", s.handleRestart)
		r.Post("/reload", s.handleReload)
		r.Post("/reinitialize", s.handleReinitialize)
		r.Post("/switchover", s.handleSwitchover)
		r.Post("/failover", s.handleFailover)
		r.Patch("/config", s.handlePatchConfig)
		r.Put("/config", s.handlePutConfig)
	})

	// Failsafe endpoint
	r.Post("/failsafe", s.handleFailsafe)

	s.router = r
}

// Start starts the API server.
func (s *Server) Start() error {
	addr := s.config.RestAPI.Listen
	if addr == "" {
		addr = ":8008"
	}

	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Info().Str("addr", addr).Msg("Starting REST API server")

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	go func() {
		if s.config.RestAPI.CertFile != "" && s.config.RestAPI.KeyFile != "" {
			if err := s.server.ServeTLS(listener, s.config.RestAPI.CertFile, s.config.RestAPI.KeyFile); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("API server error")
			}
		} else {
			if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("API server error")
			}
		}
	}()

	return nil
}

// Stop stops the API server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// authMiddleware checks authentication for protected endpoints.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth if not configured
		if s.config.RestAPI.Username == "" {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != s.config.RestAPI.Username || pass != s.config.RestAPI.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Patroni"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Response helpers

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

// getPostgreSQLStatus returns the current PostgreSQL status.
func (s *Server) getPostgreSQLStatus() map[string]interface{} {
	status := map[string]interface{}{
		"state": s.pg.State().String(),
		"role":  s.pg.Role().String(),
	}

	if s.pg.IsRunning() {
		status["server_version"] = s.pg.MajorVersion()

		timeline, walPos := s.pg.TimelineWALPosition()
		if timeline > 0 {
			status["timeline"] = timeline
		}

		if s.pg.IsPrimary() {
			status["wal"] = map[string]interface{}{
				"location": walPos,
			}
		} else {
			status["wal"] = map[string]interface{}{
				"received_location": walPos,
				"replayed_location": walPos,
			}
		}
	}

	// Add patroni info
	status["patroni"] = map[string]interface{}{
		"version": types.Version,
		"scope":   s.config.Scope,
		"name":    s.config.Name,
	}

	// Add tags
	tags := s.ha.GetEffectiveTags()
	if tags.NoFailover || tags.NoLoadbalance || tags.CloneFrom {
		status["tags"] = tags
	}

	// Add database system identifier
	if sysID := s.pg.SysID(); sysID != "" {
		status["database_system_identifier"] = sysID
	}

	// Add pending restart info
	if s.pg.IsPendingRestart() {
		status["pending_restart"] = true
		status["pending_restart_reason"] = s.pg.PendingRestartReason()
	}

	return status
}

// Health check handlers

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := s.getPostgreSQLStatus()

	httpStatus := http.StatusOK
	if !s.pg.IsRunning() {
		httpStatus = http.StatusServiceUnavailable
	}

	s.writeJSON(w, httpStatus, status)
}

func (s *Server) handlePrimary(w http.ResponseWriter, r *http.Request) {
	if s.ha.IsLeader() && s.pg.IsPrimary() {
		s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
		return
	}
	s.writeError(w, http.StatusServiceUnavailable, "not primary")
}

func (s *Server) handleReplica(w http.ResponseWriter, r *http.Request) {
	if !s.ha.IsLeader() && s.pg.IsRunning() && !s.pg.IsPrimary() {
		tags := s.ha.GetEffectiveTags()
		if !tags.NoLoadbalance {
			// Check lag parameter
			if lagParam := r.URL.Query().Get("lag"); lagParam != "" {
				// TODO: Implement lag checking
			}
			s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
			return
		}
	}
	s.writeError(w, http.StatusServiceUnavailable, "not replica")
}

func (s *Server) handleReadOnly(w http.ResponseWriter, r *http.Request) {
	if s.pg.IsRunning() {
		tags := s.ha.GetEffectiveTags()
		if !tags.NoLoadbalance {
			s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
			return
		}
	}
	s.writeError(w, http.StatusServiceUnavailable, "not available for reads")
}

func (s *Server) handleLeader(w http.ResponseWriter, r *http.Request) {
	if s.ha.IsLeader() {
		s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
		return
	}
	s.writeError(w, http.StatusServiceUnavailable, "not leader")
}

func (s *Server) handleStandbyLeader(w http.ResponseWriter, r *http.Request) {
	if s.ha.IsLeader() && !s.pg.IsPrimary() {
		s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
		return
	}
	s.writeError(w, http.StatusServiceUnavailable, "not standby leader")
}

func (s *Server) handleSynchronous(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement synchronous standby check
	s.writeError(w, http.StatusServiceUnavailable, "not synchronous standby")
}

func (s *Server) handleAsynchronous(w http.ResponseWriter, r *http.Request) {
	if !s.ha.IsLeader() && s.pg.IsRunning() && !s.pg.IsPrimary() {
		// TODO: Check if not synchronous
		s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
		return
	}
	s.writeError(w, http.StatusServiceUnavailable, "not asynchronous standby")
}

// Status handlers

func (s *Server) handlePatroni(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	cluster := s.ha.GetCluster()
	if cluster == nil {
		s.writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	response := map[string]interface{}{
		"scope": s.config.Scope,
	}

	// Add members
	members := make([]map[string]interface{}, 0)
	for _, m := range cluster.Members {
		member := map[string]interface{}{
			"name":    m.Name,
			"role":    m.Data.Role,
			"state":   m.Data.State,
			"api_url": m.Data.APIURL,
		}
		if m.Data.Timeline > 0 {
			member["timeline"] = m.Data.Timeline
		}
		if m.Data.XlogLocation > 0 {
			member["lag"] = 0 // TODO: Calculate actual lag
		}
		members = append(members, member)
	}
	response["members"] = members

	// Add leader info
	if cluster.Leader != nil {
		response["leader"] = cluster.Leader.MemberName
	}

	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	cluster := s.ha.GetCluster()
	if cluster == nil || cluster.History == nil {
		s.writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	s.writeJSON(w, http.StatusOK, cluster.History.Value)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cluster := s.ha.GetCluster()
	if cluster == nil || cluster.Config == nil {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}
	s.writeJSON(w, http.StatusOK, cluster.Config.Data)
}

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if s.pg.IsRunning() && s.pg.State() == types.PostgresqlStateRunning {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}
	http.Error(w, "Not Ready", http.StatusServiceUnavailable)
}

// Management handlers

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Restart requested via API")

	var req struct {
		Schedule   string `json:"schedule,omitempty"`
		Role       string `json:"role,omitempty"`
		PostgreSQL struct {
			PendingRestart bool `json:"pending_restart,omitempty"`
		} `json:"postgresql,omitempty"`
	}

	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	// TODO: Implement scheduled restart

	ctx := r.Context()
	if err := s.pg.Restart(ctx); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "restarted successfully"})
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Reload requested via API")

	ctx := r.Context()
	if err := s.pg.Reload(ctx); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "reloaded successfully"})
}

func (s *Server) handleReinitialize(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Reinitialize requested via API")

	if s.ha.IsLeader() {
		s.writeError(w, http.StatusForbidden, "cannot reinitialize the leader")
		return
	}

	// TODO: Implement reinitialize
	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "reinitialize scheduled"})
}

func (s *Server) handleSwitchover(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Switchover requested via API")

	var req struct {
		Leader    string `json:"leader,omitempty"`
		Candidate string `json:"candidate,omitempty"`
		Scheduled string `json:"scheduled_at,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Verify we are the leader
	if !s.ha.IsLeader() {
		s.writeError(w, http.StatusForbidden, "switchover must be sent to the leader")
		return
	}

	// TODO: Implement switchover logic
	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "switchover scheduled"})
}

func (s *Server) handleFailover(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Failover requested via API")

	var req struct {
		Leader    string `json:"leader,omitempty"`
		Candidate string `json:"candidate,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.ContentLength > 0 {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TODO: Implement failover logic
	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "failover initiated"})
}

func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Config patch requested via API")

	var patch map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TODO: Apply patch to dynamic configuration
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "configuration updated"})
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Config replace requested via API")

	var newConfig map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TODO: Replace dynamic configuration
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "configuration replaced"})
}

func (s *Server) handleFailsafe(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TODO: Update failsafe state
	log.Debug().Interface("data", data).Msg("Failsafe ping received")

	response := map[string]interface{}{
		"accepted": true,
	}

	// Add WAL position
	_, walPos := s.pg.TimelineWALPosition()
	if walPos > 0 {
		response["lsn"] = walPos
	}

	s.writeJSON(w, http.StatusOK, response)
}

// CheckAccess checks if the request is allowed based on allowlist.
func (s *Server) CheckAccess(r *http.Request) bool {
	if len(s.config.RestAPI.Allowlist) == 0 {
		return true
	}

	remoteAddr := r.RemoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		remoteAddr = remoteAddr[:idx]
	}

	for _, allowed := range s.config.RestAPI.Allowlist {
		if allowed == remoteAddr {
			return true
		}
		// TODO: Support CIDR ranges
	}

	return false
}
