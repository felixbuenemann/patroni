package postgresql

import (
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/config"
)

// CallbackAction represents the type of callback.
type CallbackAction string

const (
	CallbackOnStart      CallbackAction = "on_start"
	CallbackOnStop       CallbackAction = "on_stop"
	CallbackOnRestart    CallbackAction = "on_restart"
	CallbackOnReload     CallbackAction = "on_reload"
	CallbackOnRoleChange CallbackAction = "on_role_change"
)

// CallbackExecutor handles asynchronous execution of callbacks.
type CallbackExecutor struct {
	mu     sync.Mutex
	config config.CallbacksConfig
}

// NewCallbackExecutor creates a new callback executor.
func NewCallbackExecutor(cfg config.CallbacksConfig) *CallbackExecutor {
	return &CallbackExecutor{
		config: cfg,
	}
}

// Execute runs a callback script asynchronously.
func (ce *CallbackExecutor) Execute(action CallbackAction, role, scope string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	var script string
	switch action {
	case CallbackOnStart:
		script = ce.config.OnStart
	case CallbackOnStop:
		script = ce.config.OnStop
	case CallbackOnRestart:
		script = ce.config.OnRestart
	case CallbackOnReload:
		script = ce.config.OnReload
	case CallbackOnRoleChange:
		script = ce.config.OnRoleChange
	}

	if script == "" {
		return
	}

	go ce.executeScript(script, string(action), role, scope)
}

// executeScript runs the actual callback script.
func (ce *CallbackExecutor) executeScript(script, action, role, scope string) {
	logger := log.With().
		Str("callback", action).
		Str("script", script).
		Logger()

	logger.Info().Msg("Executing callback")

	parts := strings.Fields(script)
	if len(parts) == 0 {
		logger.Warn().Msg("Empty callback script")
		return
	}

	// Build arguments: script action role scope
	args := append(parts[1:], action, role, scope)

	cmd := exec.Command(parts[0], args...)
	cmd.Env = append(os.Environ(),
		"PATRONI_SCOPE="+scope,
		"PATRONI_ROLE="+role,
		"PATRONI_ACTION="+action,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error().
			Err(err).
			Str("output", string(output)).
			Msg("Callback failed")
		return
	}

	logger.Info().Str("output", string(output)).Msg("Callback completed")
}
