package datagetter

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// DataGetterState represents the state of a DataGetter.
type DataGetterState = int32

const (
	// DataGetterStateNotStarted indicates that the DataGetter has not yet started initialization.
	DataGetterStateNotStarted DataGetterState = 0
	// DataGetterStateStarted indicates that the DataGetter has started but not yet completed initialization.
	DataGetterStateStarted DataGetterState = 1
	// DataGetterStateDone indicates that the DataGetter has completed initialization (either successfully or with an error).
	DataGetterStateDone DataGetterState = 2
)

// InitFunc is the function type for data initialization.
// It receives a context.Context for handling timeouts and cancellations, and returns an error to indicate the result of the initialization.
type InitFunc func(ctx context.Context) error

// PanicHandler is a function type for handling panics that occur during initialization.
// It receives the value from recover() and should return an error.
type PanicHandler func(p any) error

// DataGetter is a tool for managing data fetching and synchronization.
// It ensures that data is initialized only once and can be safely shared across multiple goroutines.
// This version supports error handling, context passing, and custom panic handling.
type DataGetter struct {
	State        DataGetterState
	SelfOnce     sync.Once
	WaitDone     chan struct{}
	WaitStarted  chan struct{}
	InitErr      error // Stores the error from the initialization process (from InitFunc or a panic).
	PanicHandler PanicHandler
}

// NewDataGetter creates a new DataGetter instance.
// You can optionally provide a custom PanicHandler.
func NewDataGetter(handler ...PanicHandler) *DataGetter {
	dg := &DataGetter{}
	if len(handler) > 0 && handler[0] != nil {
		dg.PanicHandler = handler[0]
	}
	return dg
}

// Start is used when the initialization process spans multiple functions, in conjunction with Done.
// You must ensure that Done is called after Start.
func (d *DataGetter) Start() {
	if !atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		return
	}
	d.InitSelf()
	close(d.WaitStarted)
}

// Done is called after Start to indicate that the initialization has finished.
// This version executes synchronously to ensure immediate state transition and avoid unnecessary goroutine overhead.
func (d *DataGetter) Done() {
	// Since initFunc is nil, DoInit will complete the state transition immediately, so a synchronous call is safe.
	d.DoInit(context.Background(), nil)
}

// AsyncInit initializes the data asynchronously.
// The initFunc will be executed in a new goroutine.
func (d *DataGetter) AsyncInit(ctx context.Context, initFunc InitFunc) {
	if !atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		return
	}
	d.InitSelf()
	close(d.WaitStarted)
	go d.DoInit(ctx, initFunc)
}

// InitAndGet initializes and gets the data.
// If the data is already initialized, it returns the result immediately (which may be an error).
// If the data is not yet initialized, it uses the initFunc to initialize it and waits for it to complete.
// If the context is canceled during the wait, it will return context.Canceled.
func (d *DataGetter) InitAndGet(ctx context.Context, initFunc InitFunc) error {
	// Fast path: if already done, return the result immediately.
	if atomic.LoadInt32(&d.State) == DataGetterStateDone {
		return d.InitErr
	}

	d.InitSelf()

	// Try to become the initializer.
	if atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		close(d.WaitStarted)
		d.DoInit(ctx, initFunc)
	}

	// Wait for initialization to complete or for the context to be canceled.
	select {
	case <-d.WaitDone:
		return d.InitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DoInit executes the initialization function. This is the internal core logic.
// It handles panics, executes the initFunc, stores errors, and finally updates the state.
func (d *DataGetter) DoInit(ctx context.Context, initFunc InitFunc) {
	// Use a defer to ensure that no matter what happens, the state transition and notification will be completed.
	defer func() {
		if r := recover(); r != nil {
			if d.PanicHandler != nil {
				// If there is a custom panic handler, use it.
				d.InitErr = d.PanicHandler(r)
			} else {
				// Default handling: convert the panic to an error.
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				d.InitErr = fmt.Errorf("datagetter: panic recovered: %v\n%s", r, buf)
			}
		}
		// Finally, complete the process.
		atomic.StoreInt32(&d.State, DataGetterStateDone)
		close(d.WaitDone)
	}()

	if initFunc != nil {
		d.InitErr = initFunc(ctx)
	}
}

// InitSelf initializes the DataGetter's own channels.
func (d *DataGetter) InitSelf() {
	d.SelfOnce.Do(func() {
		d.WaitDone = make(chan struct{})
		d.WaitStarted = make(chan struct{})
	})
}

// CallStackWait waits for a series of DataGetters to complete initialization.
// This function's logic remains unchanged because it is concerned with the state flow, not the error of the initialization result.
// If the caller is concerned about the final result, it needs to call InitAndGet(ctx, nil) on the target DataGetter after CallStackWait returns.
func CallStackWait(ctx context.Context, path []*DataGetter) {
	if len(path) == 0 {
		return
	}
	if atomic.LoadInt32(&path[0].State) == DataGetterStateNotStarted {
		return
	}
	for idx, dataGetter := range path {
		dataGetter.InitSelf()
		if idx+1 < len(path) {
			nextDataGetter := path[idx+1]
			nextDataGetter.InitSelf()
			select {
			case <-nextDataGetter.WaitStarted:
				continue
			case <-dataGetter.WaitDone:
				if atomic.LoadInt32(&nextDataGetter.State) != DataGetterStateNotStarted {
					continue
				}
				return
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-dataGetter.WaitDone:
			return
		case <-ctx.Done():
			return
		}
	}
}
