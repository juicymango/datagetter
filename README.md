# DataGetter

[![Go Report Card](https://goreportcard.com/badge/github.com/juicymango/datagetter)](https://goreportcard.com/report/github.com/juicymango/datagetter)
[![GoDoc](https://godoc.org/github.com/juicymango/datagetter?status.svg)](https://godoc.org/github.com/juicymango/datagetter)

DataGetter is a lightweight, zero-dependency Go package that provides a robust mechanism for handling one-time data initialization in concurrent applications. It ensures that an initialization function is executed exactly once, even when called from multiple goroutines simultaneously.

## The Problem

In many concurrent programs, you have a piece of data that is expensive to produce and is only produced once. All subsequent requests for the data should wait for the production to finish and then use the already produced data. This can lead to complex and error-prone boilerplate code involving mutexes, wait groups, and channels to handle the synchronization correctly.

DataGetter solves this problem by providing a simple and reusable tool to manage this "one-time initialization" pattern.

## Features

-   **Exactly-Once Execution**: Guarantees that an initialization function is called only once.
-   **Goroutine-Safe**: Can be safely called from multiple goroutines.
-   **Zero-Value Ready**: A `DataGetter` is ready to use from its zero value. No explicit initialization is needed.
-   **Advanced Dependency Handling**: Provides `CallStackWait` to handle complex, optional dependency chains between multiple `DataGetter` instances.
-   **Context Aware**: All waiting operations respect `context.Context` for cancellation and timeouts.
-   **Panic Safe**: Recovers from panics within the initialization function and returns them as errors.

## Installation

```sh
go get github.com/juicymango/datagetter
```

## Usage

The common pattern is to embed a `DataGetter` within a struct, alongside the data it protects.

```go
type MyService struct {
    // data is the data being protected.
    // It is only safe to access after the getter has completed.
    data   string
    getter DataGetter
}
```

### Primary Usage (99% of cases)

The most common use case is having a producer start an asynchronous initialization, while one or more consumers wait for it to complete.

-   **Producer**: Calls `AsyncInit()` to start the initialization in a new goroutine.
-   **Consumer**: Calls `InitAndGet(nil)` to block until initialization is complete.

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/juicymango/datagetter"
)

// Service holds the data and the DataGetter.
type Service struct {
	importantData string
	getter        datagetter.DataGetter
}

// producer starts the data initialization process asynchronously.
func (s *Service) producer(ctx context.Context) {
	fmt.Println("Producer: Starting initialization...")
	initFunc := func(ctx context.Context) error {
		fmt.Println("InitFunc: Performing complex initialization...")
		time.Sleep(100 * time.Millisecond)
		s.importantData = "Hello, World!"
		fmt.Println("InitFunc: Initialization complete.")
		return nil
	}
	s.getter.AsyncInit(ctx, initFunc)
}

// consumer waits for the data to be initialized and then uses it.
func (s *Service) consumer(id int) {
	fmt.Printf("Consumer %d: Waiting for data...\n", id)
	err := s.getter.InitAndGet(context.Background(), nil)
	if err != nil {
		fmt.Printf("Consumer %d: Failed to get data: %v\n", id, err)
		return
	}
	// It is now safe to access the data.
	fmt.Printf("Consumer %d: Successfully got data: '%s'\n", id, s.importantData)
}

func main() {
	myService := &Service{}

	// The producer starts the process.
	myService.producer(context.Background())

	// Multiple consumers can now wait for the result.
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			myService.consumer(id)
		}(i)
	}
	wg.Wait()
}
```

### Basic Usage

If the consumer of the data is also the one responsible for initializing it, you can use `InitAndGet` directly with an `InitFunc`. `DataGetter` ensures that only the first caller will execute the function.

```go
func (s *Service) consumerAndInitializer(id int) {
	fmt.Printf("Goroutine %d: Attempting to initialize and get data...\n", id)

	initFunc := func(ctx context.Context) error {
		fmt.Printf("Goroutine %d: Running the init function!\n", id)
		s.importantData = "initialized by the first caller"
		return nil
	}

	err := s.getter.InitAndGet(context.Background(), initFunc)
	if err != nil {
		// handle error
	}
	fmt.Printf("Goroutine %d: Data is ready: '%s'\n", id, s.importantData)
}
```

### Advanced Usage: `CallStackWait`

In complex applications, you may have a chain of initializations (e.g., function A initializes data needed by B, which initializes data for C). A consumer might only depend on C, but its initialization is optional. `CallStackWait` allows a consumer to wait for the entire chain, and it will correctly unblock if any part of the chain is skipped.

See the implementation in `datagetter_test.go` for a detailed example of this scenario.

### Error Handling

If the `InitFunc` returns an error, that error will be cached and returned to all current and future callers of `InitAndGet`. When you receive an error, you should assume the associated data is in an invalid or inconsistent state.

```go
initFunc := func(ctx context.Context) error {
    // Attempt to get data, but fail.
    err := errors.New("failed to connect to database")
    if err != nil {
        return err // Return before assigning to any shared state.
    }
    // s.data = ...
    return nil
}

err := s.getter.InitAndGet(context.Background(), initFunc)
if err != nil {
    // Handle the error. Do not use the data.
    fmt.Println(err)
}
```

## API Philosophy

This package follows a specific design philosophy regarding its API:

**All fields and methods are exported.**

This provides you with the maximum freedom and flexibility to use `DataGetter` in ways that may not be covered by the standard examples. You can inspect the state, access the internal error, or build your own logic on top of the exported fields.

However, with this freedom comes responsibility. The usage patterns shown in this README and in the official test files are the recommended and officially supported ways to use this package. If you choose to use the exported fields in other ways, you are responsible for ensuring the correctness and safety of your implementation.

## Contributing

Contributions are welcome! Please feel free to open an issue or submit a pull request.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
