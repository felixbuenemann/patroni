package async

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockCancellable implements the Cancellable interface for testing.
type MockCancellable struct {
	mu           sync.Mutex
	cancelled    bool
	resetCalled  bool
	cancelCalled bool
}

func (m *MockCancellable) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelled = true
	m.cancelCalled = true
}

func (m *MockCancellable) ResetIsCancelled() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelled = false
	m.resetCalled = true
}

func (m *MockCancellable) IsCancelled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelled
}

func (m *MockCancellable) WasResetCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resetCalled
}

func (m *MockCancellable) WasCancelCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelCalled
}

func TestAsyncExecutorNew(t *testing.T) {
	cancellable := &MockCancellable{}
	wakeupCalled := false
	wakeup := func() { wakeupCalled = true }

	executor := New(cancellable, wakeup)

	if executor == nil {
		t.Fatal("New() returned nil")
	}
	if executor.State() != StateIdle {
		t.Errorf("Initial state = %v, want %v", executor.State(), StateIdle)
	}
	if executor.Busy() {
		t.Error("New executor should not be busy")
	}
	if executor.CriticalTask == nil {
		t.Error("CriticalTask should be initialized")
	}
	_ = wakeupCalled // suppress unused warning
}

func TestRunAsync(t *testing.T) {
	cancellable := &MockCancellable{}
	wakeupCalled := false
	wakeup := func() { wakeupCalled = true }

	executor := New(cancellable, wakeup)

	// Test that RunAsync executes the function
	resultCh := make(chan interface{}, 1)
	executor.RunAsync(func() interface{} {
		resultCh <- true
		return true
	})

	// Wait for result
	select {
	case result := <-resultCh:
		if result != true {
			t.Errorf("RunAsync result = %v, want true", result)
		}
	case <-time.After(time.Second):
		t.Error("RunAsync timed out")
	}

	// Wait for executor to finish
	time.Sleep(50 * time.Millisecond)

	// Verify wakeup was called (because result was non-nil)
	if !wakeupCalled {
		t.Error("wakeup should have been called")
	}

	// Verify ResetIsCancelled was called on cancellable
	if !cancellable.WasResetCalled() {
		t.Error("ResetIsCancelled should have been called on cancellable")
	}
}

func TestRun(t *testing.T) {
	cancellable := &MockCancellable{}
	executor := New(cancellable, nil)

	// Test normal execution
	result := executor.Run(func() interface{} {
		return "success"
	})
	if result != "success" {
		t.Errorf("Run() = %v, want 'success'", result)
	}

	// Test that panic is handled gracefully (like Python exception handling)
	executor2 := New(cancellable, nil)
	result = executor2.Run(func() interface{} {
		panic("test panic")
	})
	// After panic recovery, result should be nil
	if result != nil {
		t.Errorf("Run() after panic = %v, want nil", result)
	}

	// Verify state is reset after execution
	if executor.State() != StateIdle {
		t.Errorf("State after Run() = %v, want %v", executor.State(), StateIdle)
	}
}

func TestCancel(t *testing.T) {
	cancellable := &MockCancellable{}
	executor := New(cancellable, nil)

	// Test cancel when not scheduled - should return immediately
	executor.Cancel()

	// Test cancel when scheduled
	executor.Schedule("foo")
	if executor.ScheduledAction() != "foo" {
		t.Errorf("ScheduledAction() = %v, want 'foo'", executor.ScheduledAction())
	}

	// Start a long-running task
	started := make(chan struct{})
	blocked := make(chan struct{})
	executor2 := New(cancellable, nil)
	executor2.Schedule("bar")

	go func() {
		executor2.Run(func() interface{} {
			close(started)
			<-blocked // Block until test says continue
			return nil
		})
	}()

	// Wait for task to start
	<-started

	// Cancel in a goroutine (it will block waiting for task)
	cancelDone := make(chan struct{})
	go func() {
		executor2.Cancel()
		close(cancelDone)
	}()

	// Let the task complete
	close(blocked)

	// Wait for cancel to complete
	select {
	case <-cancelDone:
		// Success
	case <-time.After(time.Second):
		t.Error("Cancel() timed out")
	}

	// Verify scheduled action is cleared
	if executor2.ScheduledAction() != "" {
		t.Errorf("ScheduledAction() after cancel = %v, want empty", executor2.ScheduledAction())
	}
}

func TestSchedule(t *testing.T) {
	executor := New(nil, nil)

	// Schedule first action
	result := executor.Schedule("foo")
	if result != "" {
		t.Errorf("Schedule('foo') = %v, want empty string", result)
	}
	if executor.ScheduledAction() != "foo" {
		t.Errorf("ScheduledAction() = %v, want 'foo'", executor.ScheduledAction())
	}

	// Try to schedule second action - should return previous action
	result = executor.Schedule("bar")
	if result != "foo" {
		t.Errorf("Schedule('bar') = %v, want 'foo'", result)
	}

	// Reset and schedule again
	executor.ResetScheduledAction()
	result = executor.Schedule("bar")
	if result != "" {
		t.Errorf("Schedule('bar') after reset = %v, want empty string", result)
	}
}

func TestTryRunAsync(t *testing.T) {
	executor := New(nil, nil)

	// First try should succeed
	result := executor.TryRunAsync("foo", func() interface{} {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	if result != "" {
		t.Errorf("TryRunAsync('foo') = %v, want empty string", result)
	}

	// Second try while first is running should fail
	result = executor.TryRunAsync("bar", func() interface{} {
		return nil
	})
	if result == "" {
		t.Error("TryRunAsync('bar') should have returned error message")
	}
	if result != "Failed to run bar, foo is already in progress" {
		t.Errorf("TryRunAsync error = %v", result)
	}
}

func TestBusy(t *testing.T) {
	executor := New(nil, nil)

	if executor.Busy() {
		t.Error("New executor should not be busy")
	}

	executor.Schedule("test")
	if !executor.Busy() {
		t.Error("Executor with scheduled action should be busy")
	}

	executor.ResetScheduledAction()
	if executor.Busy() {
		t.Error("Executor after reset should not be busy")
	}
}

// TestCriticalTask tests the CriticalTask type (matches Python test_completed_task).
func TestCriticalTask(t *testing.T) {
	ct := NewCriticalTask()

	// Test initial state
	if ct.IsCancelled {
		t.Error("New CriticalTask should not be cancelled")
	}
	if ct.Result != nil {
		t.Error("New CriticalTask should have nil result")
	}

	// Test Cancel before completion
	if !ct.Cancel() {
		t.Error("Cancel() on uncompleted task should return true")
	}
	if !ct.IsCancelled {
		t.Error("Task should be cancelled after Cancel()")
	}

	// Test Reset
	ct.Reset()
	if ct.IsCancelled {
		t.Error("Task should not be cancelled after Reset()")
	}

	// Test Complete then Cancel (matches Python test_completed_task)
	ct.Complete(1)
	if ct.Result != 1 {
		t.Errorf("Result = %v, want 1", ct.Result)
	}
	if ct.Cancel() {
		t.Error("Cancel() on completed task should return false")
	}
}

func TestCriticalTaskLocking(t *testing.T) {
	ct := NewCriticalTask()

	// Test that Lock/Unlock work correctly
	ct.Lock()
	ct.Complete("test")
	ct.Unlock()

	if ct.Result != "test" {
		t.Errorf("Result = %v, want 'test'", ct.Result)
	}
}

func TestWait(t *testing.T) {
	executor := New(nil, nil)

	// Wait when not busy should return immediately
	done := make(chan struct{})
	go func() {
		executor.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("Wait() on idle executor should return immediately")
	}
}

func TestWaitWithTimeout(t *testing.T) {
	executor := New(nil, nil)

	// Wait when not busy should return true immediately
	if !executor.WaitWithTimeout(time.Millisecond) {
		t.Error("WaitWithTimeout on idle executor should return true")
	}

	// Start a long task and test timeout
	executor.Schedule("long")
	blocked := make(chan struct{})

	go executor.Run(func() interface{} {
		<-blocked
		return nil
	})

	// Give task time to start
	time.Sleep(10 * time.Millisecond)

	// Wait should timeout
	if executor.WaitWithTimeout(10 * time.Millisecond) {
		t.Error("WaitWithTimeout should return false on timeout")
	}

	// Let task complete
	close(blocked)
	time.Sleep(50 * time.Millisecond)
}

func TestRunWithContext(t *testing.T) {
	executor := New(nil, nil)
	ctx := context.Background()

	result := executor.RunWithContext(ctx, func(ctx context.Context) interface{} {
		return "context-result"
	})

	if result != "context-result" {
		t.Errorf("RunWithContext() = %v, want 'context-result'", result)
	}
}

func TestExecutorState(t *testing.T) {
	cancellable := &MockCancellable{}
	executor := New(cancellable, nil)

	// Initial state
	if executor.State() != StateIdle {
		t.Errorf("Initial State() = %v, want %v", executor.State(), StateIdle)
	}

	// Start a task and check state changes to running
	started := make(chan struct{})
	blocked := make(chan struct{})

	executor.Schedule("test")
	go executor.Run(func() interface{} {
		close(started)
		<-blocked
		return nil
	})

	<-started
	if executor.State() != StateRunning {
		t.Errorf("State during execution = %v, want %v", executor.State(), StateRunning)
	}

	close(blocked)
	time.Sleep(50 * time.Millisecond)

	if executor.State() != StateIdle {
		t.Errorf("State after execution = %v, want %v", executor.State(), StateIdle)
	}
}

func TestSetTimeout(t *testing.T) {
	executor := New(nil, nil)

	executor.SetTimeout(5 * time.Second)
	// Timeout is stored internally - verify by checking it doesn't panic
}
