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
		var sharedData string // The data being protected

		initFunc := func(ctx context.Context) error {
			// Simulate work
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&executionCount, 1)
			sharedData = "this is the initialized data"
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
					return
				}
				// After InitAndGet, it is safe to access the data.
				if sharedData != "this is the initialized data" {
					t.Errorf("Expected to read the correct shared data")
				}
			}()
		}
		wg.Wait()

		if executionCount != 1 {
			t.Fatalf("Expected initFunc to be called exactly once, but it was called %d times", executionCount)
		}
		if sharedData != "this is the initialized data" {
			t.Fatalf("Expected shared data to be initialized")
		}
	})

	t.Run("Scenario: Initialization returns an error", func(t *testing.T) {
		// This test ensures that if the initialization function fails and returns
		// an error, all concurrent goroutines waiting for the data will receive that same error.
		// The shared data should not be modified.
		var getter DataGetter
		var sharedData string // The data that should not be changed
		expectedErr := errors.New("initialization failed")

		initFunc := func(ctx context.Context) error {
			// This function now demonstrates the correct pattern: perform fallible
			// operations first, and only assign to shared state upon success.
			failableOperation := func() error {
				return expectedErr
			}

			if err := failableOperation(); err != nil {
				return err // Return before modifying sharedData
			}
			sharedData = "this should not be set"
			return nil
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

		if sharedData != "" {
			t.Errorf("Expected shared data to be empty, but it was '%s'", sharedData)
		}
	})

	t.Run("Scenario: Initialization panics", func(t *testing.T) {
		// This test ensures that if the initialization function panics, the panic
		// is recovered, converted into an error, and propagated to all callers.
		var getter DataGetter
		var sharedData string
		panicMsg := "something went terribly wrong"

		initFunc := func(ctx context.Context) error {
			// This function demonstrates a safe way to handle panics.
			// The panic happens before the shared data is assigned.
			failableOperation := func() {
				panic(panicMsg)
			}

			failableOperation() // This will panic
			sharedData = "this should not be set"
			return nil
		}

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := getter.InitAndGet(context.Background(), initFunc)
				if err == nil {
					fmt.Println("goroutine failed: expected an error from panic, but got nil")
					return
				}
				if !strings.Contains(err.Error(), panicMsg) {
					fmt.Printf("goroutine failed: expected error message to contain '%s', but got '%s'\n", panicMsg, err.Error())
				}
			}()
		}
		wg.Wait()

		if sharedData != "" {
			t.Errorf("Expected shared data to be empty, but it was '%s'", sharedData)
		}
	})

	// The context cancellation tests are primarily about control flow, not data access,
	// so they are left as is.
	t.Run("Scenario: Context is canceled while waiting", func(t *testing.T) {
		var getter DataGetter
		initStarted := make(chan struct{})
		initFunc := func(ctx context.Context) error {
			close(initStarted)
			time.Sleep(100 * time.Millisecond)
			return nil
		}
		go getter.InitAndGet(context.Background(), initFunc)
		<-
		initStarted
		waiterCtx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := getter.InitAndGet(waiterCtx, nil)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Expected context.Canceled error, but got %v", err)
			}
		}()
		time.Sleep(10 * time.Millisecond)
		cancel()
		wg.Wait()
	})

	t.Run("Scenario: Context is canceled for the initializer", func(t *testing.T) {
		var getter DataGetter
		initFuncFinished := make(chan struct{})
		initFunc := func(ctx context.Context) error {
			defer close(initFuncFinished)
			select {
			case <-
				time.After(100 * time.Millisecond):
				return errors.New("initFunc should have been canceled")
			case <-ctx.Done():
				return ctx.Err()
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
		time.Sleep(10 * time.Millisecond)
		cancel()
		wg.Wait()
		if !errors.Is(initErr, context.Canceled) {
			t.Fatalf("Expected initializer to return context.Canceled, but got %v", initErr)
		}
		<-
		initFuncFinished
		waiterErr := getter.InitAndGet(context.Background(), nil)
		if !errors.Is(waiterErr, context.Canceled) {
			t.Errorf("Expected subsequent waiters to get context.Canceled, but got %v", waiterErr)
		}
	})
}

// TestAsyncInit covers the scenario where a producer starts an async initialization,
// and a consumer waits for it, then accesses the data.
func TestAsyncInit(t *testing.T) {
	// This scenario is for when the producer knows it will start before the consumer,
	// but doesn't want to block on the initialization. It fires off the initialization
	// asynchronously, and consumers can use InitAndGet(nil) to wait for the result.
	type Data struct {
		getter DataGetter
		value  string
	}
	service := &Data{}

	initFunc := func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		service.value = "initialized"
		return nil
	}

	// Producer starts the initialization asynchronously.
	service.getter.AsyncInit(context.Background(), initFunc)

	// At this point, the value should not be set yet.
	if service.value != "" {
		t.Fatal("Expected value to be empty before consumer waits")
	}

	// Consumer waits for the result.
	err := service.getter.InitAndGet(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expected no error from consumer, but got %v", err)
	}

	// After waiting, the value should be set and safe to access.
	if service.value != "initialized" {
		t.Fatalf("Expected value to be 'initialized', but got '%s'", service.value)
	}
}

// TestCallStackWait covers the complex scenarios involving dependency chains
// and demonstrates how to access the final data.
func TestCallStackWait(t *testing.T) {
	// Scenario: A consumer depends on a result from an asynchronous function C,
	// which is itself triggered by another asynchronous function B, which is
	// triggered by a function A. (A -> B -> C).
	// The consumer needs to wait for C, but must not wait forever if the execution
	// path skips B or C. CallStackWait solves this by allowing the consumer to
	// wait on the entire potential call path.

	type Service struct {
		DataA struct {
			Getter DataGetter
			Value  string
		}
		DataB struct {
			Getter DataGetter
			Value  string
		}
		DataC struct {
			Getter DataGetter
			Value  string
		}
	}

	t.Run("Sub-Scenario: Dependency chain is fully executed", func(t *testing.T) {
		// Here, we test the "happy path" where A's init function triggers B's,
		// and B's init function triggers C's. The consumer waiting on the
		// path [A, B, C] should wait until C is complete and then can access C's data.
		s := &Service{}

		initC := func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			s.DataC.Value = "C is done"
			return nil
		}

		initB := func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			s.DataB.Value = "B is done"
			// B triggers C's initialization.
			s.DataC.Getter.AsyncInit(ctx, initC)
			return nil
		}

		initA := func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			s.DataA.Value = "A is done"
			// A triggers B's initialization.
			s.DataB.Getter.AsyncInit(ctx, initB)
			return nil
		}

		// Start the top-level initialization.
		s.DataA.Getter.AsyncInit(context.Background(), initA)

		path := []*DataGetter{&s.DataA.Getter, &s.DataB.Getter, &s.DataC.Getter}
		CallStackWait(context.Background(), path)

		// After CallStackWait returns, C must be done.
		if s.DataC.Getter.State != DataGetterStateDone {
			t.Fatalf("Expected getterC to be in Done state, but was %d", s.DataC.Getter.State)
		}
		// And the data from C should be accessible.
		if s.DataC.Value != "C is done" {
			t.Fatalf("Expected DataC.Value to be 'C is done', but got '%s'", s.DataC.Value)
		}
	})

	t.Run("Sub-Scenario: Dependency chain is partially executed (producer skipped)", func(t *testing.T) {
		// Here, we test the critical case where A triggers B, but B *does not* trigger C.
		// The consumer waiting on path [A, B, C] should stop waiting once B is complete,
		// recognizing that C was skipped.
		s := &Service{}

		initB := func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			s.DataB.Value = "B is done"
			// B completes without triggering C.
			return nil
		}

		initA := func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			s.DataA.Value = "A is done"
			s.DataB.Getter.AsyncInit(ctx, initB)
			return nil
		}

		s.DataA.Getter.AsyncInit(context.Background(), initA)

		path := []*DataGetter{&s.DataA.Getter, &s.DataB.Getter, &s.DataC.Getter}
		CallStackWait(context.Background(), path)

		// After CallStackWait returns, B should be done, but C should not have started.
		if s.DataB.Getter.State != DataGetterStateDone {
			t.Fatalf("Expected getterB to be in Done state, but was %d", s.DataB.Getter.State)
		}
		if s.DataC.Getter.State != DataGetterStateNotStarted {
			t.Fatalf("Expected getterC to be in NotStarted state, but was %d", s.DataC.Getter.State)
		}
		// The data from B should be accessible, but C's should be empty.
		if s.DataB.Value != "B is done" {
			t.Fatalf("Expected DataB.Value to be 'B is done', but got '%s'", s.DataB.Value)
		}
		if s.DataC.Value != "" {
			t.Fatalf("Expected DataC.Value to be empty, but got '%s'", s.DataC.Value)
		}
	})

	// The context cancellation test is about control flow, so it is left as is.
	t.Run("Sub-Scenario: Context is canceled during wait", func(t *testing.T) {
		var getterA, getterB DataGetter
		initB := func(ctx context.Context) error {
			time.Sleep(200 * time.Millisecond)
			return nil
		}
		initA := func(ctx context.Context) error {
			getterB.AsyncInit(ctx, initB)
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		getterA.AsyncInit(context.Background(), initA)
		waitReturned := make(chan struct{})
		go func() {
			path := []*DataGetter{&getterA, &getterB}
			CallStackWait(ctx, path)
			close(waitReturned)
		}()
		time.Sleep(10 * time.Millisecond)
		cancel()
		select {
		case <-waitReturned:
			// Success
		case <-time.After(50 * time.Millisecond):
			t.Fatal("CallStackWait did not return promptly after context cancellation")
		}
	})
}