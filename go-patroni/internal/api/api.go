// Package api implements the Patroni REST API server.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
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
	config     *config.Config
	ha         *ha.HA
	pg         *postgresql.Postgresql
	server     *http.Server
	router     chi.Router
	failsafe   map[string]string
}

// NewServer creates a new API server.
func NewServer(haInstance *ha.HA, pg *postgresql.Postgresql, cfg *config.RestAPIConfig) *Server {
	s := &Server{
		ha:       haInstance,
		pg:       pg,
		failsafe: make(map[string]string),
	}

	if cfg != nil {
		s.config = &config.Config{RestAPI: *cfg}
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
		r.Delete("/restart", s.handleDeleteRestart)
		r.Post("/reload", s.handleReload)
		r.Post("/reinitialize", s.handleReinitialize)
		r.Post("/switchover", s.handleSwitchover)
		r.Delete("/switchover", s.handleDeleteSwitchover)
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
	addr := ":8008"
	if s.config != nil && s.config.RestAPI.Listen != "" {
		addr = s.config.RestAPI.Listen
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
		if s.config != nil &&
			s.config.RestAPI.CertFile != "" && s.config.RestAPI.KeyFile != "" {
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
func (s *Server) Stop() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// ReloadConfig reloads the API configuration.
func (s *Server) ReloadConfig(cfg *config.RestAPIConfig) {
	if cfg != nil && s.config != nil {
		s.config.RestAPI = *cfg
	}
}

// authMiddleware checks authentication for protected endpoints.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth if not configured
		if s.config == nil || s.config.RestAPI.Username == "" {
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
		"scope":   s.getScope(),
		"name":    s.getName(),
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

func (s *Server) getScope() string {
	if s.config != nil {
		return s.config.Scope
	}
	return ""
}

func (s *Server) getName() string {
	if s.config != nil {
		return s.config.Name
	}
	return ""
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
				maxLag, err := strconv.ParseInt(lagParam, 10, 64)
				if err == nil {
					currentLag := s.calculateLag()
					if currentLag > maxLag {
						s.writeError(w, http.StatusServiceUnavailable,
							fmt.Sprintf("lag %d exceeds maximum %d", currentLag, maxLag))
						return
					}
				}
			}
			s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
			return
		}
	}
	s.writeError(w, http.StatusServiceUnavailable, "not replica")
}

// calculateLag calculates the replication lag in bytes.
func (s *Server) calculateLag() int64 {
	cluster := s.ha.GetCluster()
	if cluster == nil || cluster.Leader == nil {
		return 0
	}

	// Get leader's WAL position
	var leaderWalPos int64
	for _, m := range cluster.Members {
		if m.Name == cluster.Leader.MemberName {
			leaderWalPos = m.Data.XlogLocation
			break
		}
	}

	// Get our WAL position
	_, myWalPos := s.pg.TimelineWALPosition()

	if leaderWalPos > myWalPos {
		return leaderWalPos - myWalPos
	}
	return 0
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
	// Check if we are a synchronous standby
	if !s.ha.IsLeader() && s.pg.IsRunning() && !s.pg.IsPrimary() {
		cluster := s.ha.GetCluster()
		if cluster != nil && cluster.SyncState != nil {
			myName := s.getName()
			// Check if we're in the sync standby list
			for _, standby := range cluster.SyncState.SyncStandby {
				if standby == myName {
					s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
					return
				}
			}
			// Also check legacy sync field
			if cluster.SyncState.Sync == myName {
				s.writeJSON(w, http.StatusOK, s.getPostgreSQLStatus())
				return
			}
		}
	}
	s.writeError(w, http.StatusServiceUnavailable, "not synchronous standby")
}

func (s *Server) handleAsynchronous(w http.ResponseWriter, r *http.Request) {
	if !s.ha.IsLeader() && s.pg.IsRunning() && !s.pg.IsPrimary() {
		// Check that we're NOT a synchronous standby
		cluster := s.ha.GetCluster()
		if cluster != nil && cluster.SyncState != nil {
			myName := s.getName()
			// Check if we're NOT in the sync standby list
			for _, standby := range cluster.SyncState.SyncStandby {
				if standby == myName {
					s.writeError(w, http.StatusServiceUnavailable, "not asynchronous standby")
					return
				}
			}
			if cluster.SyncState.Sync == myName {
				s.writeError(w, http.StatusServiceUnavailable, "not asynchronous standby")
				return
			}
		}
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
		"scope": s.getScope(),
	}

	// Get leader WAL position for lag calculation
	var leaderWalPos int64
	if cluster.Leader != nil {
		for _, m := range cluster.Members {
			if m.Name == cluster.Leader.MemberName {
				leaderWalPos = m.Data.XlogLocation
				break
			}
		}
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
		// Calculate lag relative to leader
		if m.Data.XlogLocation > 0 && leaderWalPos > 0 {
			lag := leaderWalPos - m.Data.XlogLocation
			if lag < 0 {
				lag = 0
			}
			member["lag"] = lag
		}
		members = append(members, member)
	}
	response["members"] = members

	// Add leader info
	if cluster.Leader != nil {
		response["leader"] = cluster.Leader.MemberName
	}

	// Add paused state
	if cluster.Config != nil && cluster.Config.Data != nil {
		if paused, ok := cluster.Config.Data["pause"].(bool); ok && paused {
			response["paused"] = true
		}
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

	// Handle scheduled restart
	if req.Schedule != "" {
		scheduleTime, err := time.Parse(time.RFC3339, req.Schedule)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid schedule format, use RFC3339")
			return
		}

		if scheduleTime.Before(time.Now()) {
			s.writeError(w, http.StatusBadRequest, "scheduled time is in the past")
			return
		}

		// Store scheduled restart in HA
		s.ha.ScheduleRestart(scheduleTime, time.Time{})
		s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"message":  "restart scheduled",
			"schedule": scheduleTime.Format(time.RFC3339),
		})
		return
	}

	// Immediate restart
	ctx := r.Context()
	if err := s.pg.Restart(ctx); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "restarted successfully"})
}

func (s *Server) handleDeleteRestart(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Delete scheduled restart requested via API")

	if err := s.ha.CancelScheduledRestart(); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "scheduled restart cancelled"})
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

	var req struct {
		Force bool `json:"force,omitempty"`
	}

	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	// Check if async task is already running
	if s.ha.IsBusy() && !req.Force {
		s.writeError(w, http.StatusConflict, "another operation is in progress")
		return
	}

	// Schedule reinitialize
	ctx := r.Context()
	if err := s.ha.Reinitialize(ctx, req.Force); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "reinitialize started"})
}

func (s *Server) handleSwitchover(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Switchover requested via API")

	var req struct {
		Leader      string `json:"leader,omitempty"`
		Candidate   string `json:"candidate,omitempty"`
		ScheduledAt string `json:"scheduled_at,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate leader
	cluster := s.ha.GetCluster()
	if cluster == nil {
		s.writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	// Verify we are the leader or leader is specified
	if !s.ha.IsLeader() {
		s.writeError(w, http.StatusForbidden, "switchover must be sent to the leader")
		return
	}

	// Check paused state
	if cluster.Config != nil && cluster.Config.Data != nil {
		if paused, ok := cluster.Config.Data["pause"].(bool); ok && paused {
			if req.Candidate == "" {
				s.writeError(w, http.StatusBadRequest, "switchover is possible only to a specific candidate in paused state")
				return
			}
		}
	}

	// Handle scheduled switchover
	var scheduledAt *time.Time
	if req.ScheduledAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid scheduled_at format, use RFC3339")
			return
		}
		scheduledAt = &t
	}

	// Check if candidate is valid
	if req.Candidate != "" {
		found := false
		for _, m := range cluster.Members {
			if m.Name == req.Candidate {
				found = true
				// Check failover limitations
				if m.Data.Tags.NoFailover {
					s.writeError(w, http.StatusBadRequest, fmt.Sprintf("candidate %s has nofailover tag", req.Candidate))
					return
				}
				break
			}
		}
		if !found {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("candidate %s not found in cluster", req.Candidate))
			return
		}
	}

	// Prevent self-switchover
	if req.Candidate == s.getName() {
		s.writeError(w, http.StatusBadRequest, "switchover target and source are the same")
		return
	}

	// Write failover key to DCS
	ctx := r.Context()
	if err := s.ha.ManualFailover(ctx, req.Leader, req.Candidate, scheduledAt); err != nil {
		s.writeError(w, http.StatusConflict, "failed to write failover key into DCS")
		return
	}

	if scheduledAt != nil {
		s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"message":      "switchover scheduled",
			"scheduled_at": scheduledAt.Format(time.RFC3339),
		})
	} else {
		s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "switchover scheduled"})
	}
}

func (s *Server) handleDeleteSwitchover(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Delete switchover requested via API")

	cluster := s.ha.GetCluster()
	if cluster == nil || cluster.Failover == nil || cluster.Failover.ScheduledAt.IsZero() {
		s.writeError(w, http.StatusNotFound, "no switchover is scheduled")
		return
	}

	ctx := r.Context()
	if err := s.ha.CancelFailover(ctx); err != nil {
		s.writeError(w, http.StatusConflict, "failed to delete switchover")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "scheduled switchover deleted"})
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

	// Failover requires a candidate
	if req.Candidate == "" {
		s.writeError(w, http.StatusBadRequest, "failover could be performed only to a specific candidate")
		return
	}

	// If leader is specified, this is actually a switchover
	if req.Leader != "" {
		log.Warn().Msg("Received failover request with leader specified - performing switchover instead")
	}

	cluster := s.ha.GetCluster()
	if cluster == nil {
		s.writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	// Check paused state - failover can't be scheduled in paused state
	if cluster.Config != nil && cluster.Config.Data != nil {
		if paused, ok := cluster.Config.Data["pause"].(bool); ok && paused {
			s.writeError(w, http.StatusBadRequest, "failover can't be scheduled in the paused state")
			return
		}
	}

	// Validate candidate
	found := false
	for _, m := range cluster.Members {
		if m.Name == req.Candidate {
			found = true
			if m.Data.Tags.NoFailover {
				s.writeError(w, http.StatusBadRequest, fmt.Sprintf("candidate %s has nofailover tag", req.Candidate))
				return
			}
			break
		}
	}
	if !found {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("candidate %s not found in cluster", req.Candidate))
		return
	}

	ctx := r.Context()
	if err := s.ha.ManualFailover(ctx, req.Leader, req.Candidate, nil); err != nil {
		s.writeError(w, http.StatusConflict, "failed to write failover key into DCS")
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "failover initiated"})
}

func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Config patch requested via API")

	var patch map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get current config
	cluster := s.ha.GetCluster()
	if cluster == nil {
		s.writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}

	// Merge patch with existing config
	currentConfig := make(map[string]interface{})
	if cluster.Config != nil && cluster.Config.Data != nil {
		for k, v := range cluster.Config.Data {
			currentConfig[k] = v
		}
	}

	// Apply patch
	for k, v := range patch {
		if v == nil {
			delete(currentConfig, k)
		} else {
			currentConfig[k] = v
		}
	}

	// Update config in DCS
	ctx := r.Context()
	if err := s.ha.SetConfig(ctx, currentConfig); err != nil {
		s.writeError(w, http.StatusConflict, "failed to update configuration")
		return
	}

	s.writeJSON(w, http.StatusOK, currentConfig)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("Config replace requested via API")

	var newConfig map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Update config in DCS
	ctx := r.Context()
	if err := s.ha.SetConfig(ctx, newConfig); err != nil {
		s.writeError(w, http.StatusConflict, "failed to update configuration")
		return
	}

	s.writeJSON(w, http.StatusOK, newConfig)
}

func (s *Server) handleFailsafe(w http.ResponseWriter, r *http.Request) {
	var data map[string]string
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Update failsafe state
	for k, v := range data {
		s.failsafe[k] = v
	}

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
	if s.config == nil || len(s.config.RestAPI.Allowlist) == 0 {
		return true
	}

	remoteAddr := r.RemoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		remoteAddr = remoteAddr[:idx]
	}

	// Remove brackets from IPv6
	remoteAddr = strings.TrimPrefix(remoteAddr, "[")
	remoteAddr = strings.TrimSuffix(remoteAddr, "]")

	remoteIP := net.ParseIP(remoteAddr)
	if remoteIP == nil {
		return false
	}

	for _, allowed := range s.config.RestAPI.Allowlist {
		// Check exact match first
		if allowed == remoteAddr {
			return true
		}

		// Check CIDR match
		if strings.Contains(allowed, "/") {
			_, network, err := net.ParseCIDR(allowed)
			if err == nil && network.Contains(remoteIP) {
				return true
			}
		} else {
			// Check IP match
			allowedIP := net.ParseIP(allowed)
			if allowedIP != nil && allowedIP.Equal(remoteIP) {
				return true
			}
		}
	}

	return false
}

// GetFailsafeState returns the current failsafe state.
func (s *Server) GetFailsafeState() map[string]string {
	return s.failsafe
}
