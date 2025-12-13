// Package async provides facilities for executing asynchronous tasks.
package async

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// State represents the executor state.
type State string

const (
	StateIdle      State = "idle"
	StateRunning   State = "running"
	StateCancelled State = "cancelled"
)

// CriticalTask represents a critical task in a background process that we either
// need to cancel or get the result of.
type CriticalTask struct {
	mu          sync.Mutex
	IsCancelled bool
	Result      interface{}
}

// NewCriticalTask creates a new CriticalTask instance.
func NewCriticalTask() *CriticalTask {
	return &CriticalTask{}
}

// Reset must be called every time the background task is finished.
// Must be called from async thread. Caller must hold lock on async executor when calling.
func (t *CriticalTask) Reset() {
	t.IsCancelled = false
	t.Result = nil
}

// Cancel tries to cancel the task.
// Returns false if the task has already run, or true if it has been cancelled.
func (t *CriticalTask) Cancel() bool {
	if t.Result != nil {
		return false
	}
	t.IsCancelled = true
	return true
}

// Complete marks task as completed along with a result.
// Must be called from async thread. Caller must hold lock on task when calling.
func (t *CriticalTask) Complete(result interface{}) {
	t.Result = result
}

// Lock acquires the task lock.
func (t *CriticalTask) Lock() {
	t.mu.Lock()
}

// Unlock releases the task lock.
func (t *CriticalTask) Unlock() {
	t.mu.Unlock()
}

// Cancellable is the interface for cancellable operations (e.g., subprocess).
type Cancellable interface {
	Cancel()
	ResetIsCancelled()
}

// TaskFunc is a function that can be executed as a task.
type TaskFunc func() interface{}

// Executor is an asynchronous executor of (long) tasks.
type Executor struct {
	mu                  sync.RWMutex
	scheduledActionMu   sync.RWMutex
	cancellable         Cancellable
	haWakeup            func()
	scheduledAction     string
	isCancelled         bool
	finishCh            chan struct{}
	CriticalTask        *CriticalTask
	state               State
	timeout             time.Duration
}

// New creates a new AsyncExecutor instance.
func New(cancellable Cancellable, haWakeup func()) *Executor {
	return &Executor{
		cancellable:  cancellable,
		haWakeup:     haWakeup,
		finishCh:     make(chan struct{}, 1),
		CriticalTask: NewCriticalTask(),
		state:        StateIdle,
	}
}

// Busy returns true if there is an action scheduled to occur.
func (e *Executor) Busy() bool {
	return e.ScheduledAction() != ""
}

// State returns the current executor state.
func (e *Executor) State() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// Schedule schedules an action to be executed.
// Returns empty string if action has been successfully scheduled,
// or the previously scheduled action if any.
func (e *Executor) Schedule(action string) string {
	e.scheduledActionMu.Lock()
	defer e.scheduledActionMu.Unlock()

	if e.scheduledAction != "" {
		return e.scheduledAction
	}
	e.scheduledAction = action
	e.isCancelled = false

	// Signal that a new action is scheduled
	select {
	case e.finishCh <- struct{}{}:
	default:
	}

	return ""
}

// ScheduledAction returns the currently scheduled action, if any.
func (e *Executor) ScheduledAction() string {
	e.scheduledActionMu.RLock()
	defer e.scheduledActionMu.RUnlock()
	return e.scheduledAction
}

// ResetScheduledAction unschedules a previously scheduled action.
func (e *Executor) ResetScheduledAction() {
	e.scheduledActionMu.Lock()
	defer e.scheduledActionMu.Unlock()
	e.scheduledAction = ""
}

// SetTimeout sets the timeout for task execution.
func (e *Executor) SetTimeout(timeout time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.timeout = timeout
}

// Run runs a function synchronously.
// Expected to be executed through a goroutine.
func (e *Executor) Run(fn TaskFunc) interface{} {
	var wakeup interface{}

	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("action", e.ScheduledAction()).
				Msg("Panic during execution of long running task")
		}

		e.mu.Lock()
		e.ResetScheduledAction()
		e.state = StateIdle
		// Signal completion
		select {
		case e.finishCh <- struct{}{}:
		default:
		}
		e.CriticalTask.Lock()
		e.CriticalTask.Reset()
		e.CriticalTask.Unlock()
		e.mu.Unlock()

		if wakeup != nil && e.haWakeup != nil {
			e.haWakeup()
		}
	}()

	e.mu.Lock()
	if e.isCancelled {
		e.mu.Unlock()
		return nil
	}
	e.state = StateRunning
	// Drain finish channel
	select {
	case <-e.finishCh:
	default:
	}
	e.mu.Unlock()

	if e.cancellable != nil {
		e.cancellable.ResetIsCancelled()
	}

	// Execute the function
	wakeup = fn()
	return wakeup
}

// RunAsync starts an async goroutine that runs the function.
func (e *Executor) RunAsync(fn TaskFunc) {
	go e.Run(fn)
}

// TryRunAsync tries to run an async task if none is currently being executed.
// Returns empty string if function was scheduled successfully,
// otherwise an error message informing of an already ongoing task.
func (e *Executor) TryRunAsync(action string, fn TaskFunc) string {
	prev := e.Schedule(action)
	if prev == "" {
		e.RunAsync(fn)
		return ""
	}
	return "Failed to run " + action + ", " + prev + " is already in progress"
}

// Cancel requests cancellation of a scheduled async task.
// Waits until task is cancelled before returning.
func (e *Executor) Cancel() {
	e.mu.Lock()
	e.scheduledActionMu.RLock()
	action := e.scheduledAction
	e.scheduledActionMu.RUnlock()

	if action == "" {
		e.mu.Unlock()
		return
	}

	log.Warn().Str("action", action).Msg("Cancelling long running task")
	e.isCancelled = true
	e.state = StateCancelled
	e.mu.Unlock()

	if e.cancellable != nil {
		e.cancellable.Cancel()
	}

	// Wait for task to finish
	<-e.finishCh

	e.mu.Lock()
	e.ResetScheduledAction()
	e.mu.Unlock()
}

// RunWithContext runs a function with context support for cancellation.
func (e *Executor) RunWithContext(ctx context.Context, fn func(context.Context) interface{}) interface{} {
	return e.Run(func() interface{} {
		return fn(ctx)
	})
}

// RunAsyncWithContext starts an async goroutine with context support.
func (e *Executor) RunAsyncWithContext(ctx context.Context, fn func(context.Context) interface{}) {
	go e.RunWithContext(ctx, fn)
}

// Wait waits for the current task to complete.
func (e *Executor) Wait() {
	if !e.Busy() {
		return
	}
	<-e.finishCh
	// Put signal back for other waiters
	select {
	case e.finishCh <- struct{}{}:
	default:
	}
}

// WaitWithTimeout waits for the current task to complete with a timeout.
func (e *Executor) WaitWithTimeout(timeout time.Duration) bool {
	if !e.Busy() {
		return true
	}

	select {
	case <-e.finishCh:
		// Put signal back
		select {
		case e.finishCh <- struct{}{}:
		default:
		}
		return true
	case <-time.After(timeout):
		return false
	}
}
