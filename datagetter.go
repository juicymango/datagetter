package datagetter

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// DataGetterState represents the state of a DataGetter.
// It uses atomic operations for thread-safe state management.
type DataGetterState = int32

// Constants defining the possible states of a DataGetter.
const (
	// DataGetterStateNotStarted indicates the DataGetter has not begun initialization.
	DataGetterStateNotStarted DataGetterState = 0
	
	// DataGetterStateStarted indicates the DataGetter has begun initialization but not completed.
	DataGetterStateStarted DataGetterState = 1
	
	// DataGetterStateDone indicates the DataGetter has completed initialization (successfully or with error).
	DataGetterStateDone DataGetterState = 2
)

// InitFunc is the function type for data initialization.
// It receives a context for cancellation/timeout support and returns an error.
// This is the primary way users define their initialization logic.
type InitFunc func(ctx context.Context) error

// PanicHandler is a function type for custom panic handling.
// It receives the panic value and should return an appropriate error.
// Users can provide this to customize how panics are converted to errors.
type PanicHandler func(p any) error

// DataGetter is the core struct that manages concurrent data initialization.
// It ensures initialization happens only once, handles errors and panics,
// and provides context-aware waiting mechanisms.
//
// The design philosophy exposes all fields publicly, giving users maximum
// flexibility while providing clear examples for common usage patterns.
type DataGetter struct {
	// State represents the current initialization state using atomic operations.
	// Users should not modify this directly but can read it for monitoring.
	State DataGetterState
	
	// SelfOnce ensures that internal channels are initialized only once.
	// This is used internally and should not be modified by users.
	SelfOnce sync.Once
	
	// WaitDone is closed when initialization completes (successfully or with error).
	// Users can wait on this channel directly for advanced use cases.
	WaitDone chan struct{}
	
	// WaitStarted is closed when initialization begins.
	// This is primarily used internally by CallStackWait.
	WaitStarted chan struct{}
	
	// initErr stores the error result from the initialization process.
	// This includes errors returned by InitFunc or converted from panics.
	// Once set, this value becomes immutable and is returned to all callers.
	initErr error
	
	// panicHandler is an optional custom function for handling panics.
	// If provided, it will be called when a panic occurs during initialization.
	// If nil, a default panic handler will be used.
	panicHandler PanicHandler
}

// NewDataGetter creates a new DataGetter instance.
// It optionally accepts a custom PanicHandler for specialized panic recovery.
// If no handler is provided, a default one will be used that converts
// panics to descriptive errors including stack traces.
//
// Example:
//   getter := NewDataGetter() // Uses default panic handling
//   getter := NewDataGetter(myCustomHandler) // Uses custom panic handling
func NewDataGetter(handler ...PanicHandler) *DataGetter {
	dg := &DataGetter{}
	if len(handler) > 0 && handler[0] != nil {
		dg.panicHandler = handler[0]
	}
	return dg
}

// Start marks the beginning of a multi-function initialization process.
// This should be used when initialization spans multiple functions and
// you need to ensure the initialization state is properly managed.
// You must guarantee that Done() will be called after Start().
//
// This method is thread-safe and idempotent - calling it multiple times
// has no effect after the first successful call.
//
// Example usage:
//   func initializeComplexSystem(getter *DataGetter) {
//       getter.Start()
//       defer getter.Done()
//       
//       // Complex initialization across multiple functions
//       step1()
//       step2()
//   }
func (d *DataGetter) Start() {
	// Use atomic compare-and-swap to ensure only one caller becomes the initializer
	if !atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		return
	}
	d.InitSelf()
	close(d.WaitStarted)
}

// Done marks the end of a multi-function initialization process started with Start().
// This method synchronously completes the initialization process without
// spawning a goroutine, ensuring immediate state transition.
//
// Since no InitFunc is provided, this simply transitions the state to Done
// and closes the WaitDone channel to notify all waiters.
//
// This should always be called after Start() to ensure proper cleanup.
func (d *DataGetter) Done() {
	// Pass nil InitFunc and background context since we're just completing the state transition
	d.DoInit(context.Background(), nil)
}

// AsyncInit starts asynchronous initialization in a new goroutine.
// This is useful when you want to begin initialization without blocking
// the current goroutine, but still need to wait for completion later.
//
// The InitFunc will execute in a separate goroutine, and its context
// will be respected for cancellation and timeout.
//
// Example:
//   getter.AsyncInit(ctx, func(ctx context.Context) error {
//       return expensiveAsyncOperation(ctx)
//   })
//   // Do other work...
//   err := getter.InitAndGet(ctx, nil) // Wait for completion
func (d *DataGetter) AsyncInit(ctx context.Context, initFunc InitFunc) {
	// Atomically claim the initialization responsibility
	if !atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		return
	}
	d.InitSelf()
	close(d.WaitStarted)
	
	// Launch the initialization in a new goroutine
	go d.DoInit(ctx, initFunc)
}

// InitAndGet is the primary method for initializing and accessing data.
// It ensures the initialization happens only once and returns the result.
//
// If initialization has already completed, it immediately returns the cached result.
// If initialization is in progress, it waits for completion.
// If initialization hasn't started, it becomes the initializer.
//
// The context parameter allows for cancellation and timeout of the wait operation.
// If the context is cancelled before initialization completes, context.Err() is returned.
//
// Returns the error from initialization (including panics converted to errors).
func (d *DataGetter) InitAndGet(ctx context.Context, initFunc InitFunc) error {
	// Fast path: if already done, return the cached result immediately
	if atomic.LoadInt32(&d.State) == DataGetterStateDone {
		return d.initErr
	}

	// Ensure internal channels are initialized
	d.InitSelf()

	// Try to become the initializer if not already started
	if atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		close(d.WaitStarted)
		d.DoInit(ctx, initFunc)
	}

	// Wait for initialization to complete or context cancellation
	select {
	case <-d.WaitDone:
		return d.initErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DoInit is the internal core method that executes initialization functions.
// It handles panic recovery, error storage, and state transition.
// This method should not be called directly by users in most cases.
//
// The defer block ensures that regardless of how the initialization ends
// (normal return, panic, or context cancellation), the state is properly
// updated and waiters are notified.
func (d *DataGetter) DoInit(ctx context.Context, initFunc InitFunc) {
	// Use defer to guarantee state cleanup happens in all cases
	defer func() {
		if r := recover(); r != nil {
			// Handle panic using custom handler or default
			if d.panicHandler != nil {
				d.initErr = d.panicHandler(r)
			} else {
				// Default panic handling: capture stack and create descriptive error
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				d.initErr = fmt.Errorf("datagetter: panic recovered: %v\n%s", r, buf)
			}
		}
		// Final state transition and notification
		atomic.StoreInt32(&d.State, DataGetterStateDone)
		close(d.WaitDone)
	}()

	// Execute the user-provided initialization function if provided
	if initFunc != nil {
		d.initErr = initFunc(ctx)
	}
}

// InitSelf initializes the internal channels of the DataGetter.
// This method uses sync.Once to ensure channels are created only once.
// It is called automatically when needed and should not be called by users.
func (d *DataGetter) InitSelf() {
	d.SelfOnce.Do(func() {
		d.WaitDone = make(chan struct{})
		d.WaitStarted = make(chan struct{})
	})
}

// CallStackWait waits for a chain of DataGetter dependencies to complete.
// This is used for managing initialization dependencies between different
// components that may have complex call relationships.
//
// The path parameter should be a slice of DataGetter pointers representing
// the dependency chain, typically in call order (parent to child).
//
// This function respects context cancellation and will return early if
// the context is done before all dependencies complete.
//
// Note: This function only waits for the initialization process to reach
// certain states; it does not return initialization errors. Callers should
// still call InitAndGet on the final DataGetter to get the actual result.
func CallStackWait(ctx context.Context, path []*DataGetter) {
	if len(path) == 0 {
		return
	}
	
	// If the first DataGetter hasn't started, there's nothing to wait for
	if atomic.LoadInt32(&path[0].State) == DataGetterStateNotStarted {
		return
	}
	
	// Iterate through the dependency chain
	for idx, dataGetter := range path {
		dataGetter.InitSelf()
		
		if idx+1 < len(path) {
			nextDataGetter := path[idx+1]
			nextDataGetter.InitSelf()
			
			// Wait for either the next DataGetter to start or the current one to complete
			select {
			case <-nextDataGetter.WaitStarted:
				// Next DataGetter has started, move to waiting for it
				continue
			case <-dataGetter.WaitDone:
				// Current DataGetter completed, check if next was skipped
				if atomic.LoadInt32(&nextDataGetter.State) != DataGetterStateNotStarted {
					continue
				}
				// Next DataGetter was never started, we're done waiting
				return
			case <-ctx.Done():
				return
			}
		}
		
		// Wait for the final DataGetter in the chain to complete
		select {
		case <-dataGetter.WaitDone:
			return
		case <-ctx.Done():
			return
		}
	}
}