// Package postgresql provides PostgreSQL management functionality.
// This file contains cancellable subprocess execution for PostgreSQL operations.
package postgresql

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

var (
	// ErrCancelled is returned when an operation was cancelled.
	ErrCancelled = errors.New("operation cancelled")
)

// CancellableExecutor manages a single cancellable subprocess.
type CancellableExecutor struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	cmdArgs     []string
	childPIDs   []int
}

// NewCancellableExecutor creates a new CancellableExecutor.
func NewCancellableExecutor() *CancellableExecutor {
	return &CancellableExecutor{}
}

// startProcess starts a subprocess with the given command.
func (e *CancellableExecutor) startProcess(ctx context.Context, name string, args []string, opts ...ProcessOption) error {
	e.childPIDs = nil
	e.cmdArgs = append([]string{name}, args...)

	e.cmd = exec.CommandContext(ctx, name, args...)

	for _, opt := range opts {
		opt(e.cmd)
	}

	if err := e.cmd.Start(); err != nil {
		log.Error().Err(err).Strs("cmd", e.cmdArgs).Msg("Failed to execute command")
		return err
	}

	return nil
}

// killProcess kills the running process and its children.
func (e *CancellableExecutor) killProcess() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cmd == nil || e.cmd.Process == nil {
		return
	}

	// Try to get children before killing
	if len(e.childPIDs) == 0 {
		e.childPIDs = getChildPIDs(e.cmd.Process.Pid)
	}

	// Kill the main process
	if err := e.cmd.Process.Kill(); err != nil {
		if !errors.Is(err, os.ErrProcessDone) {
			log.Warn().Err(err).Strs("cmd", e.cmdArgs).Msg("Failed to kill process")
		}
	} else {
		log.Warn().Strs("cmd", e.cmdArgs).Msg("Killed process because it was still running")
	}
}

// killChildren kills child processes.
func (e *CancellableExecutor) killChildren() {
	e.mu.Lock()
	pids := e.childPIDs
	e.mu.Unlock()

	for _, pid := range pids {
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.Debug().Err(err).Int("pid", pid).Msg("Failed to kill child process")
		}
	}
}

// ProcessOption configures a process before execution.
type ProcessOption func(*exec.Cmd)

// WithStdin sets the stdin for the process.
func WithStdin(stdin *bytes.Buffer) ProcessOption {
	return func(cmd *exec.Cmd) {
		cmd.Stdin = stdin
	}
}

// WithStdout sets the stdout for the process.
func WithStdout(stdout *bytes.Buffer) ProcessOption {
	return func(cmd *exec.Cmd) {
		cmd.Stdout = stdout
	}
}

// WithStderr sets the stderr for the process.
func WithStderr(stderr *bytes.Buffer) ProcessOption {
	return func(cmd *exec.Cmd) {
		cmd.Stderr = stderr
	}
}

// WithDir sets the working directory for the process.
func WithDir(dir string) ProcessOption {
	return func(cmd *exec.Cmd) {
		cmd.Dir = dir
	}
}

// WithEnv sets additional environment variables for the process.
func WithEnv(env []string) ProcessOption {
	return func(cmd *exec.Cmd) {
		cmd.Env = append(os.Environ(), env...)
	}
}

// CancellableSubprocess extends CancellableExecutor with cancellation support.
type CancellableSubprocess struct {
	*CancellableExecutor
	mu          sync.Mutex
	isCancelled bool
	cancelCtx   context.Context
	cancelFunc  context.CancelFunc
}

// NewCancellableSubprocess creates a new CancellableSubprocess.
func NewCancellableSubprocess() *CancellableSubprocess {
	ctx, cancel := context.WithCancel(context.Background())
	return &CancellableSubprocess{
		CancellableExecutor: NewCancellableExecutor(),
		cancelCtx:          ctx,
		cancelFunc:         cancel,
	}
}

// Call executes a command and waits for it to complete.
func (c *CancellableSubprocess) Call(name string, args []string, opts ...ProcessOption) (int, error) {
	c.mu.Lock()
	if c.isCancelled {
		c.mu.Unlock()
		return -1, ErrCancelled
	}
	c.isCancelled = false

	// Create a new context for this call
	ctx, cancel := context.WithCancel(c.cancelCtx)
	defer cancel()

	if err := c.startProcess(ctx, name, args, opts...); err != nil {
		c.mu.Unlock()
		c.killChildren()
		return -1, err
	}
	c.mu.Unlock()

	// Wait for the process
	err := c.cmd.Wait()

	c.mu.Lock()
	c.CancellableExecutor.cmd = nil
	c.mu.Unlock()

	c.killChildren()

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}

	return 0, nil
}

// CallWithCommunicate executes a command with stdin/stdout/stderr capture.
func (c *CancellableSubprocess) CallWithCommunicate(name string, args []string, input string) (int, string, string, error) {
	var stdin, stdout, stderr bytes.Buffer
	if input != "" {
		if input[len(input)-1] != '\n' {
			input += "\n"
		}
		stdin.WriteString(input)
	}

	opts := []ProcessOption{
		WithStdin(&stdin),
		WithStdout(&stdout),
		WithStderr(&stderr),
	}

	exitCode, err := c.Call(name, args, opts...)
	return exitCode, stdout.String(), stderr.String(), err
}

// ResetIsCancelled resets the cancelled state.
func (c *CancellableSubprocess) ResetIsCancelled() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isCancelled = false

	// Create a new cancel context
	c.cancelFunc()
	c.cancelCtx, c.cancelFunc = context.WithCancel(context.Background())
}

// IsCancelled returns whether the subprocess was cancelled.
func (c *CancellableSubprocess) IsCancelled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isCancelled
}

// Cancel cancels the running subprocess.
func (c *CancellableSubprocess) Cancel(kill bool) {
	c.mu.Lock()
	c.isCancelled = true

	cmd := c.CancellableExecutor.cmd
	if cmd == nil || cmd.Process == nil {
		c.mu.Unlock()
		return
	}

	log.Info().Strs("cmd", c.cmdArgs).Msg("Terminating process")

	// Send SIGTERM first
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		if !errors.Is(err, os.ErrProcessDone) {
			log.Warn().Err(err).Msg("Failed to send SIGTERM")
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Wait for graceful shutdown
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		cmd := c.CancellableExecutor.cmd
		if cmd == nil || cmd.Process == nil {
			c.mu.Unlock()
			return
		}
		// Check if process is still running
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()

		if kill {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Force kill if still running
	c.killProcess()
}

// getChildPIDs gets the PIDs of child processes.
// This is a simplified implementation - on Linux we could read /proc.
func getChildPIDs(parentPID int) []int {
	// Note: A full implementation would use /proc on Linux or
	// platform-specific APIs. For now, return empty slice.
	return nil
}
