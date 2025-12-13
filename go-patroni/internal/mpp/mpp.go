// Package mpp provides the Multi-Part Processing (MPP) abstraction for distributed
// PostgreSQL systems like Citus.
package mpp

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
)

// Type represents the type of MPP handler.
type Type string

const (
	TypeNull  Type = "null"
	TypeCitus Type = "citus"
)

// CoordinatorGroupID is the group ID for the Citus coordinator.
const CoordinatorGroupID = 0

// Handler is the interface for MPP handlers.
type Handler interface {
	// Type returns the MPP type.
	Type() Type

	// Group returns the group number for this node.
	Group() *int

	// CoordinatorGroupID returns the coordinator's group ID.
	CoordinatorGroupID() int

	// IsCoordinator returns true if this node is a coordinator.
	IsCoordinator() bool

	// IsWorker returns true if this node is a worker.
	IsWorker() bool

	// HandleEvent handles an MPP-specific event.
	HandleEvent(ctx context.Context, event string) error

	// SyncMetaData synchronizes metadata across the cluster.
	SyncMetaData(ctx context.Context) error

	// OnDemote handles demotion events.
	OnDemote(ctx context.Context) error

	// ScheduleCacheRebuild schedules a cache rebuild.
	ScheduleCacheRebuild(ctx context.Context) error

	// Bootstrap performs initial MPP setup.
	Bootstrap(ctx context.Context) error

	// AdjustPostgresGUCs adjusts PostgreSQL GUCs for MPP.
	AdjustPostgresGUCs(ctx context.Context, gucs map[string]interface{}) map[string]interface{}

	// IgnoreReplicationSlot returns true if the slot should be ignored.
	IgnoreReplicationSlot(slot map[string]interface{}) bool

	// ValidateConfig validates MPP configuration.
	ValidateConfig(config map[string]interface{}) error
}

// Config holds MPP configuration.
type Config struct {
	Type     Type                   `yaml:"type" json:"type"`
	Group    *int                   `yaml:"group" json:"group"`
	Database string                 `yaml:"database" json:"database"`
	Options  map[string]interface{} `yaml:"options" json:"options"`
}

// GetHandler returns the appropriate MPP handler based on configuration.
func GetHandler(config *Config) (Handler, error) {
	if config == nil {
		return NewNullHandler(), nil
	}

	switch config.Type {
	case TypeCitus:
		return NewCitusHandler(config), nil
	case TypeNull, "":
		return NewNullHandler(), nil
	default:
		log.Warn().Str("type", string(config.Type)).Msg("Unknown MPP type, using null handler")
		return NewNullHandler(), nil
	}
}

// GetHandlerFromMap returns an MPP handler from a configuration map.
func GetHandlerFromMap(config map[string]interface{}) (Handler, error) {
	if config == nil {
		return NewNullHandler(), nil
	}

	// Check for Citus configuration
	if citusConfig, ok := config["citus"].(map[string]interface{}); ok {
		cfg := &Config{
			Type:    TypeCitus,
			Options: citusConfig,
		}
		if group, ok := citusConfig["group"].(int); ok {
			cfg.Group = &group
		}
		if db, ok := citusConfig["database"].(string); ok {
			cfg.Database = db
		}
		return NewCitusHandler(cfg), nil
	}

	return NewNullHandler(), nil
}

// NullHandler is a no-op MPP handler.
type NullHandler struct{}

// NewNullHandler creates a new null handler.
func NewNullHandler() *NullHandler {
	return &NullHandler{}
}

// Type returns the handler type.
func (h *NullHandler) Type() Type {
	return TypeNull
}

// Group returns nil for null handler.
func (h *NullHandler) Group() *int {
	return nil
}

// CoordinatorGroupID returns -1 for null handler.
func (h *NullHandler) CoordinatorGroupID() int {
	return -1
}

// IsCoordinator returns false for null handler.
func (h *NullHandler) IsCoordinator() bool {
	return false
}

// IsWorker returns false for null handler.
func (h *NullHandler) IsWorker() bool {
	return false
}

// HandleEvent is a no-op for null handler.
func (h *NullHandler) HandleEvent(ctx context.Context, event string) error {
	return nil
}

// SyncMetaData is a no-op for null handler.
func (h *NullHandler) SyncMetaData(ctx context.Context) error {
	return nil
}

// OnDemote is a no-op for null handler.
func (h *NullHandler) OnDemote(ctx context.Context) error {
	return nil
}

// ScheduleCacheRebuild is a no-op for null handler.
func (h *NullHandler) ScheduleCacheRebuild(ctx context.Context) error {
	return nil
}

// Bootstrap is a no-op for null handler.
func (h *NullHandler) Bootstrap(ctx context.Context) error {
	return nil
}

// AdjustPostgresGUCs returns gucs unchanged for null handler.
func (h *NullHandler) AdjustPostgresGUCs(ctx context.Context, gucs map[string]interface{}) map[string]interface{} {
	return gucs
}

// IgnoreReplicationSlot returns false for null handler.
func (h *NullHandler) IgnoreReplicationSlot(slot map[string]interface{}) bool {
	return false
}

// ValidateConfig always returns nil for null handler.
func (h *NullHandler) ValidateConfig(config map[string]interface{}) error {
	return nil
}

// CitusHandler handles Citus-specific MPP operations.
type CitusHandler struct {
	config *Config
}

// NewCitusHandler creates a new Citus handler.
func NewCitusHandler(config *Config) *CitusHandler {
	return &CitusHandler{config: config}
}

// Type returns the handler type.
func (h *CitusHandler) Type() Type {
	return TypeCitus
}

// Group returns the group number for this node.
func (h *CitusHandler) Group() *int {
	return h.config.Group
}

// CoordinatorGroupID returns the coordinator's group ID.
func (h *CitusHandler) CoordinatorGroupID() int {
	return CoordinatorGroupID
}

// IsCoordinator returns true if this node is a coordinator.
func (h *CitusHandler) IsCoordinator() bool {
	if h.config.Group == nil {
		return false
	}
	return *h.config.Group == CoordinatorGroupID
}

// IsWorker returns true if this node is a worker.
func (h *CitusHandler) IsWorker() bool {
	if h.config.Group == nil {
		return false
	}
	return *h.config.Group > CoordinatorGroupID
}

// HandleEvent handles Citus-specific events.
func (h *CitusHandler) HandleEvent(ctx context.Context, event string) error {
	log.Debug().Str("event", event).Msg("Citus handling event")
	// Citus-specific event handling would go here
	return nil
}

// SyncMetaData synchronizes Citus metadata.
func (h *CitusHandler) SyncMetaData(ctx context.Context) error {
	if !h.IsCoordinator() {
		return nil
	}
	log.Debug().Msg("Syncing Citus metadata")
	// Metadata sync logic would go here
	return nil
}

// OnDemote handles Citus-specific demotion.
func (h *CitusHandler) OnDemote(ctx context.Context) error {
	log.Debug().Msg("Citus handling demotion")
	// Demotion logic would go here
	return nil
}

// ScheduleCacheRebuild schedules a Citus cache rebuild.
func (h *CitusHandler) ScheduleCacheRebuild(ctx context.Context) error {
	log.Debug().Msg("Scheduling Citus cache rebuild")
	// Cache rebuild logic would go here
	return nil
}

// Bootstrap performs initial Citus setup.
func (h *CitusHandler) Bootstrap(ctx context.Context) error {
	log.Debug().Msg("Bootstrapping Citus")
	// Bootstrap logic would go here
	return nil
}

// AdjustPostgresGUCs adjusts PostgreSQL GUCs for Citus.
func (h *CitusHandler) AdjustPostgresGUCs(ctx context.Context, gucs map[string]interface{}) map[string]interface{} {
	if gucs == nil {
		gucs = make(map[string]interface{})
	}

	// Add Citus to shared_preload_libraries if not present
	if spl, ok := gucs["shared_preload_libraries"].(string); ok {
		if spl == "" {
			gucs["shared_preload_libraries"] = "citus"
		} else if !containsCitus(spl) {
			gucs["shared_preload_libraries"] = spl + ",citus"
		}
	} else {
		gucs["shared_preload_libraries"] = "citus"
	}

	return gucs
}

func containsCitus(libraries string) bool {
	for _, lib := range splitLibraries(libraries) {
		if lib == "citus" {
			return true
		}
	}
	return false
}

func splitLibraries(libraries string) []string {
	result := []string{}
	for _, lib := range split(libraries, ",") {
		lib = trim(lib)
		if lib != "" {
			result = append(result, lib)
		}
	}
	return result
}

func split(s, sep string) []string {
	var result []string
	for _, part := range strings.Split(s, sep) {
		result = append(result, part)
	}
	return result
}

func trim(s string) string {
	return strings.TrimSpace(s)
}

// IgnoreReplicationSlot returns true if the slot should be ignored for Citus.
func (h *CitusHandler) IgnoreReplicationSlot(slot map[string]interface{}) bool {
	// Citus uses specific slot naming patterns
	if name, ok := slot["name"].(string); ok {
		// Ignore Citus internal slots
		if strings.HasPrefix(name, "citus_") {
			return true
		}
	}
	return false
}

// ValidateConfig validates Citus configuration.
func (h *CitusHandler) ValidateConfig(config map[string]interface{}) error {
	// Citus-specific validation would go here
	return nil
}
