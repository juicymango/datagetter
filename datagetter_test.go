package datagetter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestBasicInitialization tests the fundamental one-time initialization behavior.
func TestBasicInitialization(t *testing.T) {
	getter := NewDataGetter()
	callCount := 0
	
	initFunc := func(ctx context.Context) error {
		callCount++
		return nil
	}
	
	// Call InitAndGet multiple times concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := getter.InitAndGet(context.Background(), initFunc)
			if err != nil {
				t.Errorf("InitAndGet failed: %v", err)
			}
		}()
	}
	wg.Wait()
	
	// Ensure initFunc was called only once
	if callCount != 1 {
		t.Errorf("Expected initFunc to be called once, got %d calls", callCount)
	}
	
	// Verify state is marked as done
	if getter.State != DataGetterStateDone {
		t.Errorf("Expected state DataGetterStateDone, got %d", getter.State)
	}
}

// TestErrorPropagation tests that errors from InitFunc are properly propagated.
func TestErrorPropagation(t *testing.T) {
	getter := NewDataGetter()
	expectedErr := errors.New("initialization failed")
	
	initFunc := func(ctx context.Context) error {
		return expectedErr
	}
	
	// Call InitAndGet
	err := getter.InitAndGet(context.Background(), initFunc)
	
	// Verify the error is returned
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
	
	// Subsequent calls should return the same error
	err2 := getter.InitAndGet(context.Background(), nil)
	if err2 != expectedErr {
		t.Errorf("Expected cached error %v, got %v", expectedErr, err2)
	}
}

// TestPanicRecovery tests that panics are properly recovered and converted to errors.
func TestPanicRecovery(t *testing.T) {
	getter := NewDataGetter()
	
	initFunc := func(ctx context.Context) error {
		panic("test panic")
	}
	
	// Call InitAndGet - should not panic but return an error
	err := getter.InitAndGet(context.Background(), initFunc)
	if err == nil {
		t.Error("Expected error from panic, got nil")
	}
	
	// Error should contain panic information
	if err.Error() == "" {
		t.Error("Expected non-empty error message from panic")
	}
}

// TestCustomPanicHandler tests custom panic handling.
func TestCustomPanicHandler(t *testing.T) {
	expectedErr := errors.New("custom panic handled")
	
	customHandler := func(p any) error {
		return expectedErr
	}
	
	getter := NewDataGetter(customHandler)
	
	initFunc := func(ctx context.Context) error {
		panic("test panic")
	}
	
	// Call InitAndGet - should use custom handler
	err := getter.InitAndGet(context.Background(), initFunc)
	if err != expectedErr {
		t.Errorf("Expected custom error %v, got %v", expectedErr, err)
	}
}

// TestContextCancellation tests that context cancellation is respected.
func TestContextCancellation(t *testing.T) {
	getter := NewDataGetter()
	
	// Create a context that will be cancelled quickly
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	
	// InitFunc that takes a long time
	initFunc := func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	
	// Call InitAndGet with short timeout
	err := getter.InitAndGet(ctx, initFunc)
	
	// Should return context deadline exceeded
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

// TestAsyncInit tests asynchronous initialization.
func TestAsyncInit(t *testing.T) {
	getter := NewDataGetter()
	callCount := 0
	
	initFunc := func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond) // Simulate work
		callCount++
		return nil
	}
	
	// Start async initialization
	getter.AsyncInit(context.Background(), initFunc)
	
	// Wait a bit for async init to start
	time.Sleep(10 * time.Millisecond)
	
	// State should be started
	if getter.State != DataGetterStateStarted {
		t.Errorf("Expected state DataGetterStateStarted, got %d", getter.State)
	}
	
	// Wait for completion
	err := getter.InitAndGet(context.Background(), nil)
	if err != nil {
		t.Errorf("InitAndGet failed: %v", err)
	}
	
	// Verify initFunc was called once
	if callCount != 1 {
		t.Errorf("Expected initFunc to be called once, got %d calls", callCount)
	}
}

// TestStartDone tests the Start/Done pattern for multi-function initialization.
func TestStartDone(t *testing.T) {
	getter := NewDataGetter()
	
	// Start initialization
	getter.Start()
	
	// State should be started
	if getter.State != DataGetterStateStarted {
		t.Errorf("Expected state DataGetterStateStarted, got %d", getter.State)
	}
	
	// WaitStarted should be closed
	select {
	case <-getter.WaitStarted:
		// Good - channel is closed
	default:
		t.Error("WaitStarted channel should be closed after Start()")
	}
	
	// Complete initialization
	getter.Done()
	
	// State should be done
	if getter.State != DataGetterStateDone {
		t.Errorf("Expected state DataGetterStateDone, got %d", getter.State)
	}
	
	// WaitDone should be closed
	select {
	case <-getter.WaitDone:
		// Good - channel is closed
	default:
		t.Error("WaitDone channel should be closed after Done()")
	}
}

// TestCallStackWaitBasic tests basic CallStackWait functionality.
func TestCallStackWaitBasic(t *testing.T) {
	getter1 := NewDataGetter()
	getter2 := NewDataGetter()
	
	// Start the first getter
	getter1.Start()
	
	// CallStackWait should return immediately if first getter hasn't started next
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	path := []*DataGetter{getter1, getter2}
	CallStackWait(ctx, path)
	
	// Complete first getter
	getter1.Done()
	
	// Now CallStackWait should complete since getter2 was never started
	CallStackWait(ctx, path)
}

// TestCallStackWaitDependencyChain tests CallStackWait with actual dependencies.
func TestCallStackWaitDependencyChain(t *testing.T) {
	getterA := NewDataGetter()
	getterB := NewDataGetter()
	getterC := NewDataGetter()
	
	// Start the chain asynchronously
	go func() {
		getterA.Start()
		time.Sleep(20 * time.Millisecond)
		
		// Start B while A is still running
		getterB.AsyncInit(context.Background(), func(ctx context.Context) error {
			time.Sleep(30 * time.Millisecond)
			return nil
		})
		
		time.Sleep(10 * time.Millisecond)
		getterA.Done()
	}()
	
	// Wait for the dependency chain
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	
	path := []*DataGetter{getterA, getterB, getterC}
	CallStackWait(ctx, path)
	
	// Verify getterB completed but getterC was skipped
	if getterB.State != DataGetterStateDone {
		t.Error("Expected getterB to be done")
	}
	
	if getterC.State != DataGetterStateNotStarted {
		t.Error("Expected getterC to not be started")
	}
}

// TestConcurrentAccess tests heavy concurrent access to ensure thread safety.
func TestConcurrentAccess(t *testing.T) {
	getter := NewDataGetter()
	callCount := 0
	
	initFunc := func(ctx context.Context) error {
		// Simulate some work
		time.Sleep(10 * time.Millisecond)
		callCount++
		return nil
	}
	
	// Launch many concurrent accessors
	var wg sync.WaitGroup
	results := make(chan error, 100)
	
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := getter.InitAndGet(context.Background(), initFunc)
			results <- err
		}(i)
	}
	
	wg.Wait()
	close(results)
	
	// Check all results
	for err := range results {
		if err != nil {
			t.Errorf("Unexpected error from concurrent access: %v", err)
		}
	}
	
	// Ensure initFunc was called only once
	if callCount != 1 {
		t.Errorf("Expected initFunc to be called once, got %d calls", callCount)
	}
}

// TestZeroValue tests that DataGetter works correctly with zero value.
func TestZeroValue(t *testing.T) {
	// Test with zero-value DataGetter (not created via NewDataGetter)
	var getter DataGetter
	
	callCount := 0
	initFunc := func(ctx context.Context) error {
		callCount++
		return nil
	}
	
	// Should work fine
	err := getter.InitAndGet(context.Background(), initFunc)
	if err != nil {
		t.Errorf("InitAndGet failed: %v", err)
	}
	
	if callCount != 1 {
		t.Errorf("Expected initFunc to be called once, got %d calls", callCount)
	}
}

// TestMultipleErrorTypes tests various error scenarios.
func TestMultipleErrorTypes(t *testing.T) {
	testCases := []struct {
		name     string
		initFunc InitFunc
		checkErr func(error) bool
	}{
		{
			name: "nil error",
			initFunc: func(ctx context.Context) error {
				return nil
			},
			checkErr: func(err error) bool { return err == nil },
		},
		{
			name: "custom error",
			initFunc: func(ctx context.Context) error {
				return errors.New("custom error")
			},
			checkErr: func(err error) bool { return err != nil && err.Error() == "custom error" },
		},
		{
			name: "context cancelled",
			initFunc: func(ctx context.Context) error {
				return context.Canceled
			},
			checkErr: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			getter := NewDataGetter()
			err := getter.InitAndGet(context.Background(), tc.initFunc)
			if !tc.checkErr(err) {
				t.Errorf("Test case %s failed: got %v", tc.name, err)
			}
		})
	}
}

// BenchmarkInitAndGet benchmarks the performance of InitAndGet.
func BenchmarkInitAndGet(b *testing.B) {
	getter := NewDataGetter()
	
	initFunc := func(ctx context.Context) error {
		return nil
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This will use the fast path after first call
		getter.InitAndGet(context.Background(), initFunc)
	}
}

// BenchmarkConcurrentInit benchmarks concurrent initialization.
func BenchmarkConcurrentInit(b *testing.B) {
	getter := NewDataGetter()
	
	initFunc := func(ctx context.Context) error {
		time.Sleep(time.Microsecond) // Small delay
		return nil
	}
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			getter.InitAndGet(context.Background(), initFunc)
		}
	})
}