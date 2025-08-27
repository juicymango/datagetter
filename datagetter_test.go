package datagetter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDataGetter_InitAndGet_Success(t *testing.T) {
	var getter DataGetter
	var counter int
	initFunc := func(ctx context.Context) error {
		counter++
		return nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
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

	if counter != 1 {
		t.Errorf("Expected initFunc to be called once, but got %d", counter)
	}
}

func TestDataGetter_InitAndGet_Error(t *testing.T) {
	var getter DataGetter
	expectedErr := errors.New("initialization failed")
	initFunc := func(ctx context.Context) error {
		return expectedErr
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := getter.InitAndGet(context.Background(), initFunc)
			if err != expectedErr {
				t.Errorf("Expected error %v, but got %v", expectedErr, err)
			}
		}()
	}
	wg.Wait()
}

func TestDataGetter_InitAndGet_Panic(t *testing.T) {
	var getter DataGetter
	initFunc := func(ctx context.Context) error {
		panic("initialization panicked")
	}

	err := getter.InitAndGet(context.Background(), initFunc)
	if err == nil {
		t.Fatal("Expected an error from panic, but got nil")
	}

	// Check that the error message contains the panic message
	if !strings.Contains(err.Error(), "initialization panicked") {
		t.Errorf("Expected error message to contain 'initialization panicked', but got %q", err.Error())
	}
}

func TestDataGetter_InitAndGet_CustomPanicHandler(t *testing.T) {
	customErr := errors.New("custom panic error")
	getter := NewDataGetter(func(p any) error {
		return customErr
	})

	initFunc := func(ctx context.Context) error {
		panic("initialization panicked")
	}

	err := getter.InitAndGet(context.Background(), initFunc)
	if err != customErr {
		t.Errorf("Expected custom error %v, but got %v", customErr, err)
	}
}

func TestDataGetter_InitAndGet_ContextCancellation(t *testing.T) {
	var getter DataGetter
	initFunc := func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := getter.InitAndGet(ctx, initFunc)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, but got %v", err)
	}
}

func TestDataGetter_AsyncInit(t *testing.T) {
	var getter DataGetter
	var initialized bool
	initFunc := func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		initialized = true
		return nil
	}

	getter.AsyncInit(context.Background(), initFunc)

	if initialized {
		t.Fatal("Expected initialization to be running in the background")
	}

	err := getter.InitAndGet(context.Background(), nil)
	if err != nil {
		t.Errorf("Expected no error, but got %v", err)
	}

	if !initialized {
		t.Fatal("Expected initialization to be complete")
	}
}

func TestDataGetter_Start_Done(t *testing.T) {
	var getter DataGetter

	getter.Start()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := getter.InitAndGet(context.Background(), nil)
		if err != nil {
			t.Errorf("Expected no error, but got %v", err)
		}
	}()

	// Give the goroutine a chance to start waiting
	time.Sleep(10 * time.Millisecond)

	getter.Done()
	wg.Wait()

	if getter.State != DataGetterStateDone {
		t.Errorf("Expected state to be Done, but got %d", getter.State)
	}
}

func TestCallStackWait(t *testing.T) {
	t.Run("all functions called", func(t *testing.T) {
		var getterA, getterB, getterC DataGetter
		started := make(chan struct{})

		go func() {
			close(started)
			getterA.Start()
			defer getterA.Done()
			time.Sleep(10 * time.Millisecond)

			getterB.Start()
			defer getterB.Done()
			time.Sleep(10 * time.Millisecond)

			getterC.Start()
			defer getterC.Done()
			time.Sleep(10 * time.Millisecond)
		}()

		<-started
		path := []*DataGetter{&getterA, &getterB, &getterC}
		CallStackWait(context.Background(), path)

		if getterC.State != DataGetterStateDone {
			t.Errorf("Expected getterC to be done, but its state is %d", getterC.State)
		}
	})

	t.Run("last function not called", func(t *testing.T) {
		var getterA, getterB, getterC DataGetter
		started := make(chan struct{})

		go func() {
			close(started)
			getterA.Start()
			defer getterA.Done()
			time.Sleep(10 * time.Millisecond)

			getterB.Start()
			defer getterB.Done()
			time.Sleep(10 * time.Millisecond)
		}()

		<-started
		path := []*DataGetter{&getterA, &getterB, &getterC}
		CallStackWait(context.Background(), path)

		if getterB.State != DataGetterStateDone {
			t.Errorf("Expected getterB to be done, but its state is %d", getterB.State)
		}
		if getterC.State != DataGetterStateNotStarted {
			t.Errorf("Expected getterC to be not started, but its state is %d", getterC.State)
		}
	})
}