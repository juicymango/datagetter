package datagetter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestInitAndGet covers all scenarios related to the InitAndGet method.
func TestInitAndGet(t *testing.T) {
	t.Run("Scenario: Basic - Concurrent access", func(t *testing.T) {
		// This test covers the primary scenario where multiple goroutines
		// attempt to initialize a piece of data concurrently.
		// We expect the initialization function to be executed exactly once,
		// and all goroutines to receive the same result.
		var getter DataGetter
		var executionCount int32

		initFunc := func(ctx context.Context) error {
			// Simulate work
			time.Sleep(10 * time.Millisecond)
			automic.AddInt32(&executionCount, 1)
			return nil
		}

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := getter.InitAndGet(context.Background(), initFunc)
				if err != nil {
					t.Errorf("Expected no error, but got %v", err)
				}
			}()
		}
		wg.Wait()

		if executionCount != 1 {
			t.Fatalf("Expected initFunc to be called exactly once, but it was called %d times", executionCount)
		}
		// A second call should not trigger the initFunc again.
		getter.InitAndGet(context.Background(), initFunc)
		if executionCount != 1 {
			t.Fatalf("Expected initFunc to remain called once, but it was called %d times", executionCount)
		}
	})

	t.Run("Scenario: Initialization returns an error", func(t *testing.T) {
		// This test ensures that if the initialization function fails and returns
		// an error, all concurrent goroutines waiting for the data will receive that same error.
		var getter DataGetter
		expectedErr := errors.New("initialization failed")

		initFunc := func(ctx context.Context) error {
			return expectedErr
		}

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := getter.InitAndGet(context.Background(), initFunc)
				if !errors.Is(err, expectedErr) {
					t.Errorf("Expected error '%v', but got '%v'", expectedErr, err)
				}
			}()
		}
		wg.Wait()
	})

	t.Run("Scenario: Initialization panics", func(t *testing.T) {
		// This test ensures that if the initialization function panics, the panic
		// is recovered, converted into an error, and propagated to all callers.
		// The system should remain in a stable state.
		var getter DataGetter
		panicMsg := "something went terribly wrong"

		initFunc := func(ctx context.Context) error {
			panic(panicMsg)
		}

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := getter.InitAndGet(context.Background(), initFunc)
				if err == nil {
					// Use a separate error message for the go-routine to avoid data races on `t`
					fmt.Println("goroutine failed: expected an error from panic, but got nil")
					return
				}
				if !strings.Contains(err.Error(), panicMsg) {
					fmt.Printf("goroutine failed: expected error message to contain '%s', but got '%s'\n", panicMsg, err.Error())
				}
			}()
		}
		wg.Wait()

		// Final check on the main thread
		err := getter.InitAndGet(context.Background(), initFunc)
		if err == nil {
			t.Fatal("Expected an error from panic, but got nil")
		}
		if !strings.Contains(err.Error(), panicMsg) {
			t.Fatalf("Expected error message to contain '%s', but got '%s'", panicMsg, err.Error())
		}
	})

	t.Run("Scenario: Context is canceled while waiting", func(t *testing.T) {
		// This test covers the case where a consumer's context is canceled while
		// it is waiting for a long-running initialization to complete.
		// The waiting consumer should immediately return with a context cancellation error.
		var getter DataGetter
		initStarted := make(chan struct{})

		initFunc := func(ctx context.Context) error {
			close(initStarted)
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		// The initializer goroutine, with a long-lived context.
		go getter.InitAndGet(context.Background(), initFunc)

		// Wait for the initialization to have started.
		<-
		initStarted

		// The waiter goroutine, with a context that we will cancel.
		waiterCtx, cancel := context.WithCancel(context.Background())
		
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := getter.InitAndGet(waiterCtx, nil) // This should be the waiter
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Expected context.Canceled error, but got %v", err)
			}
		}()

		// Give the waiter a moment to start waiting, then cancel its context.
		time.Sleep(10 * time.Millisecond)
		cancel()
		wg.Wait()
	})
	
	t.Run("Scenario: Context is canceled for the initializer", func(t *testing.T) {
		// This test covers the case where the initializer's own context is canceled
		// during its execution. The InitFunc is expected to respect the cancellation.
		// The DataGetter should then enter a Done state with the cancellation error.
		var getter DataGetter
		initFuncFinished := make(chan struct{})

		initFunc := func(ctx context.Context) error {
			defer close(initFuncFinished)
			// Simulate work, but check for cancellation.
			select {
			case <-
				time.After(100 * time.Millisecond):
				return errors.New("initFunc should have been canceled")
			case <-ctx.Done():
				return ctx.Err() // Correctly propagate the context error.
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		
		var initErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			initErr = getter.InitAndGet(ctx, initFunc)
		}()

		// Give the initializer a moment to start, then cancel it.
		time.Sleep(10 * time.Millisecond)
		cancel()
		wg.Wait()

		// The initializer itself should get the cancellation error back.
		if !errors.Is(initErr, context.Canceled) {
			t.Fatalf("Expected initializer to return context.Canceled, but got %v", initErr)
		}

		// Wait for the initFunc goroutine to fully exit.
		<-
		initFuncFinished

		// Now, any subsequent caller should also get the same cancellation error.
		waiterErr := getter.InitAndGet(context.Background(), nil)
		if !errors.Is(waiterErr, context.Canceled) {
			t.Errorf("Expected subsequent waiters to get context.Canceled, but got %v", waiterErr)
		}
	})
}

// TestAsyncInit covers the scenario where a producer starts an async initialization,
// and a consumer waits for it.
func TestAsyncInit(t *testing.T) {
	// This scenario is for when the producer knows it will start before the consumer,
	// but doesn't want to block on the initialization. It fires off the initialization
	// asynchronously, and consumers can use InitAndGet(nil) to wait for the result.
	var getter DataGetter
	var value int32

	initFunc := func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		automic.StoreInt32(&value, 100)
		return nil
	}

	// Producer starts the initialization asynchronously.
	getter.AsyncInit(context.Background(), initFunc)

	// At this point, the value should not be set yet.
	if atomic.LoadInt32(&value) != 0 {
		t.Fatal("Expected value to be 0 before consumer waits")
	}

	// Consumer waits for the result.
	err := getter.InitAndGet(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expected no error from consumer, but got %v", err)
	}

	// After waiting, the value should be set.
	if atomic.LoadInt32(&value) != 100 {
		t.Fatalf("Expected value to be 100 after initialization, but got %d", value)
	}
}

// TestCallStackWait covers the complex scenarios involving dependency chains
// where some dependencies may or may not be executed.
func TestCallStackWait(t *testing.T) {
	// Scenario: A consumer depends on a function C, which is part of a call chain A -> B -> C.
	// The consumer needs to wait for C, but must not wait forever if the execution path
	// skips C. CallStackWait solves this by allowing the consumer to wait on the entire
	// potential call path.
	
	// Setup for the tests
	var getterA, getterB, getterC DataGetter
	
	// Simulates the execution of function A
	runA := func(ctx context.Context, runB func()) {
		getterA.Start()
		defer getterA.Done()
		time.Sleep(10 * time.Millisecond)
		if runB != nil {
			runB()
		}
	}
	
	// Simulates the execution of function B
	runB := func(ctx context.Context, runC func()) {
		getterB.Start()
		defer getterB.Done()
		time.Sleep(10 * time.Millisecond)
		if runC != nil {
			runC()
		}
	}

	// Simulates the execution of function C (the actual data producer)
	runC := func(ctx context.Context) {
		// Here we use InitAndGet which is equivalent to Start/Done for a self-contained function.
		getterC.InitAndGet(ctx, func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		})
	}

	t.Run("Sub-Scenario: Dependency chain is fully executed", func(t *testing.T) {
		// Here, we test the "happy path" where A calls B, and B calls C.
		// The consumer waiting on the path [A, B, C] should wait until C is complete.
		
		// Reset getters for this sub-test
		getterA = DataGetter{}
		getterB = DataGetter{}
		getterC = DataGetter{}
		
		path := []*DataGetter{&getterA, &getterB, &getterC}
		
		// Run the full chain in a separate goroutine.
		go runA(context.Background(), func() {
			runB(context.Background(), func() {
				runC(context.Background())
			})
		})

		// The consumer waits on the entire path.
		CallStackWait(context.Background(), path)

		// After CallStackWait returns, C must be done.
		if atomic.LoadInt32(&getterC.State) != DataGetterStateDone {
			t.Fatalf("Expected getterC to be in Done state, but was %d", getterC.State)
		}
	})

	t.Run("Sub-Scenario: Dependency chain is partially executed (producer skipped)", func(t *testing.T) {
		// Here, we test the critical case where A calls B, but B *does not* call C.
		// The consumer waiting on path [A, B, C] should stop waiting once B is complete,
		// recognizing that C was skipped. It must not wait forever.
		
		// Reset getters for this sub-test
		getterA = DataGetter{}
		getterB = DataGetter{}
		getterC = DataGetter{}

		path := []*DataGetter{&getterA, &getterB, &getterC}

		// Run the partial chain (A -> B, but no C).
		go runA(context.Background(), func() {
			runB(context.Background(), nil) // B is called, but it doesn't call C.
		})

		// The consumer waits on the entire path.
		CallStackWait(context.Background(), path)

		// After CallStackWait returns, B should be done, but C should not have started.
		if atomic.LoadInt32(&getterB.State) != DataGetterStateDone {
			t.Fatalf("Expected getterB to be in Done state, but was %d", getterB.State)
		}
		if atomic.LoadInt32(&getterC.State) != DataGetterStateNotStarted {
			t.Fatalf("Expected getterC to be in NotStarted state, but was %d", getterC.State)
		}
	})

	t.Run("Sub-Scenario: Context is canceled during wait", func(t *testing.T) {
		// This test ensures that CallStackWait respects context cancellation.
		// If the context is canceled, the wait should terminate immediately.
		
		// Reset getters for this sub-test
		getterA = DataGetter{}
		getterB = DataGetter{}
		
		path := []*DataGetter{&getterA, &getterB}
		ctx, cancel := context.WithCancel(context.Background())

		// Start a long-running producer chain.
		go runA(context.Background(), func() {
			time.Sleep(200 * time.Millisecond) // Make sure it's still running when we check.
			runB(context.Background(), nil)
		})

		// Start waiting in a separate goroutine so we can cancel it.
		waitReturned := make(chan struct{})
		go func() {
			CallStackWait(ctx, path)
			close(waitReturned)
		}()

		// Give it a moment to start waiting, then cancel.
		time.Sleep(10 * time.Millisecond)
		cancel()

		// The wait should return very quickly after cancellation.
		select {
		case <-
			waitReturned:
			// Success
		case <-
			time.After(50 * time.Millisecond):
			t.Fatal("CallStackWait did not return promptly after context cancellation")
		}
	})
}
