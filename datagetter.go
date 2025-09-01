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
	// DataGetterStateDone indicates that the DataGetter has completed initialization (whether successfully or not).
	DataGetterStateDone DataGetterState = 2
)

// InitFunc is the function type for data initialization.
// It receives a context.Context for handling timeouts and cancellations,
// and returns an error to indicate the result of the initialization.
type InitFunc func(ctx context.Context) error

// DataGetter is a tool for managing data fetching and synchronization.
// It ensures that data is initialized only once and can be safely shared among multiple goroutines.
// The zero value of a DataGetter is ready to use.
type DataGetter struct {
	State       DataGetterState
	SelfOnce    sync.Once
	WaitDone    chan struct{}
	WaitStarted chan struct{}
	InitErr     error
}

// Start is used for initialization processes that span across functions, used in conjunction with Done.
// The user must ensure that Done is called after Start.
func (d *DataGetter) Start() {
	if !atomic.CompareAndSwapInt32(&d.State, DataGetterStateNotStarted, DataGetterStateStarted) {
		return
	}
	d.InitSelf()
	close(d.WaitStarted)
}

// Done is called after Start to indicate that the initialization has ended.
// This is a synchronous call to ensure the state transition is immediate.
func (d *DataGetter) Done() {
	d.DoInit(context.Background(), nil)
}

// AsyncInit initializes data asynchronously.
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
// If the data has already been initialized, it returns the result immediately (which may be an error).
// If the data has not been initialized, it uses initFunc to initialize it and waits for completion.
// If the context is canceled during the wait, it returns context.Canceled.
func (d *DataGetter) InitAndGet(ctx context.Context, initFunc InitFunc) error {
	if atomic.LoadInt32(&d.State) == DataGetterStateDone {
		return d.InitErr
	}

	d.InitSelf()

	d.AsyncInit(ctx, initFunc)

	select {
	case <-d.WaitDone:
		return d.InitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DoInit executes the initialization function. This is the internal core logic.
// It handles panics, executes the initFunc, stores any resulting error, and updates the state.
func (d *DataGetter) DoInit(ctx context.Context, initFunc InitFunc) {
	defer func() {
		if r := recover(); r != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			d.InitErr = fmt.Errorf("datagetter: panic recovered: %v\n%s", r, buf)
		}
		atomic.StoreInt32(&d.State, DataGetterStateDone)
		close(d.WaitDone)
	}()

	if initFunc != nil {
		d.InitErr = initFunc(ctx)
	}
}

// InitSelf initializes the channels within the DataGetter.
// It's called by other methods to ensure the DataGetter is ready.
func (d *DataGetter) InitSelf() {
	d.SelfOnce.Do(func() {
		d.WaitDone = make(chan struct{})
		d.WaitStarted = make(chan struct{})
	})
}

// CallStackWait waits for a series of DataGetters to complete initialization.
// This function is used to handle dependency chains.
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
