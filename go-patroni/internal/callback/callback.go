// Package callback provides facilities for executing lifecycle callbacks.
package callback

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// Action represents a callback action type.
type Action string

const (
	ActionNoop         Action = "noop"
	ActionOnStart      Action = "on_start"
	ActionOnStop       Action = "on_stop"
	ActionOnRestart    Action = "on_restart"
	ActionOnReload     Action = "on_reload"
	ActionOnRoleChange Action = "on_role_change"
)

// Role represents a PostgreSQL role.
type Role string

const (
	RolePrimary       Role = "primary"
	RoleReplica       Role = "replica"
	RoleStandbyLeader Role = "standby_leader"
)

// DefaultTimeout is the default timeout for callback execution.
const DefaultTimeout = 30 * time.Second

// Config holds callback configuration.
type Config struct {
	Script     string
	Timeout    time.Duration
	MaxRetries int
	RetryDelay time.Duration
}

// Environment holds environment variables passed to callbacks.
type Environment struct {
	Scope   string
	Name    string
	Role    Role
	Action  Action
	ConnURL string
}

// ToEnv converts the environment to a slice of environment variables.
func (e *Environment) ToEnv() []string {
	env := os.Environ()
	env = append(env,
		fmt.Sprintf("PATRONI_SCOPE=%s", e.Scope),
		fmt.Sprintf("PATRONI_NAME=%s", e.Name),
		fmt.Sprintf("PATRONI_ROLE=%s", e.Role),
		fmt.Sprintf("PATRONI_ACTION=%s", e.Action),
	)
	if e.ConnURL != "" {
		env = append(env, fmt.Sprintf("PATRONI_CONN_URL=%s", e.ConnURL))
	}
	return env
}

// Result represents the result of a callback execution.
type Result struct {
	ExitCode int
	Output   string
	Error    error
	Duration time.Duration
}

// Success returns true if the callback executed successfully.
func (r *Result) Success() bool {
	return r.ExitCode == 0 && r.Error == nil
}

// Executor executes callbacks.
type Executor struct {
	mu               sync.Mutex
	cond             *sync.Cond
	cmd              []string
	process          *exec.Cmd
	processLock      sync.Mutex
	onReloadExecutor *OnReloadExecutor
	running          bool
	stopCh           chan struct{}
	doneCh           chan struct{}
}

// NewExecutor creates a new callback executor.
func NewExecutor() *Executor {
	e := &Executor{
		onReloadExecutor: NewOnReloadExecutor(),
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
	}
	e.cond = sync.NewCond(&e.mu)
	go e.run()
	return e
}

// Call executes a callback command.
// Already running command is killed (including child processes).
// If it couldn't be killed we wait until it finishes.
func (e *Executor) Call(cmd []string) {
	log.Debug().Strs("cmd", cmd).Msg("CallbackExecutor.Call")

	// Check if this is an on_reload callback
	if len(cmd) >= 3 && cmd[len(cmd)-3] == string(ActionOnReload) {
		e.onReloadExecutor.CallNoWait(cmd)
		return
	}

	e.killProcess()
	e.mu.Lock()
	e.cmd = cmd
	e.cond.Signal()
	e.mu.Unlock()
}

// CallWithEnv executes a callback with the given environment.
func (e *Executor) CallWithEnv(script string, action Action, env *Environment) *Result {
	if script == "" {
		return &Result{ExitCode: 0}
	}

	env.Action = action
	cmd := []string{script, string(action), string(env.Role), env.Scope}

	start := time.Now()
	result := e.execute(cmd, env.ToEnv(), DefaultTimeout)
	result.Duration = time.Since(start)

	return result
}

func (e *Executor) run() {
	defer close(e.doneCh)

	for {
		e.mu.Lock()
		for e.cmd == nil {
			select {
			case <-e.stopCh:
				e.mu.Unlock()
				return
			default:
			}
			e.cond.Wait()
		}
		cmd := e.cmd
		e.cmd = nil
		e.mu.Unlock()

		if cmd != nil {
			e.processLock.Lock()
			e.process = exec.Command(cmd[0], cmd[1:]...)
			e.process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := e.process.Start(); err != nil {
				log.Error().Err(err).Strs("cmd", cmd).Msg("Failed to start callback")
				e.processLock.Unlock()
				continue
			}
			e.processLock.Unlock()

			if e.process != nil {
				e.process.Wait()
				e.killChildren()
			}
		}
	}
}

func (e *Executor) execute(cmd []string, env []string, timeout time.Duration) *Result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	proc := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	proc.Env = env
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	output, err := proc.CombinedOutput()

	result := &Result{
		Output: string(output),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = -1
			result.Error = fmt.Errorf("callback timed out after %v", timeout)
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Error = err
		} else {
			result.ExitCode = -1
			result.Error = err
		}
	}

	return result
}

func (e *Executor) killProcess() {
	e.processLock.Lock()
	defer e.processLock.Unlock()

	if e.process == nil || e.process.Process == nil {
		return
	}

	// Kill the process group
	pgid, err := syscall.Getpgid(e.process.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		e.process.Process.Kill()
	}
}

func (e *Executor) killChildren() {
	e.processLock.Lock()
	defer e.processLock.Unlock()

	if e.process == nil || e.process.Process == nil {
		return
	}

	// Kill any remaining children in the process group
	pgid, err := syscall.Getpgid(e.process.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// Stop stops the callback executor.
func (e *Executor) Stop() {
	close(e.stopCh)
	e.cond.Signal()
	<-e.doneCh
	e.onReloadExecutor.Stop()
}

// OnReloadExecutor handles on_reload callbacks specially.
// It runs at most one on_reload callback at a time, killing any previous one.
type OnReloadExecutor struct {
	mu      sync.Mutex
	process *exec.Cmd
	stopCh  chan struct{}
}

// NewOnReloadExecutor creates a new on_reload executor.
func NewOnReloadExecutor() *OnReloadExecutor {
	return &OnReloadExecutor{
		stopCh: make(chan struct{}),
	}
}

// CallNoWait runs one on_reload callback at most.
// It always kills already running command including child processes.
func (e *OnReloadExecutor) CallNoWait(cmd []string) {
	e.cancel(true)
	e.killChildren()

	e.mu.Lock()
	e.process = exec.Command(cmd[0], cmd[1:]...)
	e.process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := e.process.Start(); err != nil {
		log.Error().Err(err).Strs("cmd", cmd).Msg("Failed to start on_reload callback")
		e.mu.Unlock()
		return
	}
	proc := e.process
	e.mu.Unlock()

	// Wait for process in background
	go proc.Wait()
}

func (e *OnReloadExecutor) cancel(kill bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.process == nil || e.process.Process == nil {
		return
	}

	if kill {
		pgid, err := syscall.Getpgid(e.process.Process.Pid)
		if err == nil {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			e.process.Process.Kill()
		}
	} else {
		e.process.Process.Signal(syscall.SIGTERM)
	}
}

func (e *OnReloadExecutor) killChildren() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.process == nil || e.process.Process == nil {
		return
	}

	pgid, err := syscall.Getpgid(e.process.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// Stop stops the on_reload executor.
func (e *OnReloadExecutor) Stop() {
	close(e.stopCh)
	e.cancel(true)
}

// ExecuteCallback is a convenience function to execute a single callback.
func ExecuteCallback(script string, action Action, role Role, scope, name, connURL string, timeout time.Duration) *Result {
	if script == "" {
		return &Result{ExitCode: 0}
	}

	if timeout == 0 {
		timeout = DefaultTimeout
	}

	env := &Environment{
		Scope:   scope,
		Name:    name,
		Role:    role,
		Action:  action,
		ConnURL: connURL,
	}

	cmd := []string{script, string(action), string(role), scope}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	proc := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	proc.Env = env.ToEnv()
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	start := time.Now()
	output, err := proc.CombinedOutput()

	result := &Result{
		Output:   string(output),
		Duration: time.Since(start),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = -1
			result.Error = fmt.Errorf("callback timed out after %v", timeout)
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Error = err
		} else {
			result.ExitCode = -1
			result.Error = err
		}
	}

	return result
}

// ExecuteWithRetry executes a callback with retry support.
func ExecuteWithRetry(script string, action Action, role Role, scope, name, connURL string,
	timeout time.Duration, maxRetries int, retryDelay time.Duration) *Result {

	var result *Result

	for i := 0; i <= maxRetries; i++ {
		result = ExecuteCallback(script, action, role, scope, name, connURL, timeout)
		if result.Success() {
			return result
		}

		if i < maxRetries {
			log.Warn().
				Int("attempt", i+1).
				Int("maxRetries", maxRetries).
				Err(result.Error).
				Msg("Callback failed, retrying")
			time.Sleep(retryDelay)
		}
	}

	return result
}
