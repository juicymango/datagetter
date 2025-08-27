# DataGetter

[![Go Report Card](https://goreportcard.com/badge/github.com/juicymango/datagetter)](https://goreportcard.com/report/github.com/juicymango/datagetter)
[![GoDoc](https://godoc.org/github.com/juicymango/datagetter?status.svg)](https://godoc.org/github.com/juicymango/datagetter)
[![Build Status](https://travis-ci.org/juicymango/datagetter.svg?branch=main)](https://travis-ci.org/juicymango/datagetter)
[![Test Coverage](https://codecov.io/gh/juicymango/datagetter/branch/main/graph/badge.svg)](https://codecov.io/gh/juicymango/datagetter)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

DataGetter is a powerful and flexible Go package for managing data initialization and synchronization. It ensures that data is initialized only once and can be safely shared across multiple goroutines, even in complex, real-world scenarios.

## The Problem

In large, concurrent applications, it's common to have data that is expensive to produce and is needed by multiple parts of the system. This often leads to complex and error-prone code to handle questions like:

- Is the data already initialized?
- What happens if multiple goroutines try to initialize the data at the same time?
- How do we handle initialization failures?
- How do we wait for data that is being initialized in another goroutine, especially when the initialization path is not guaranteed to be executed?

DataGetter provides a robust and well-tested solution to these problems.

## Installation

```bash
go get github.com/juicymango/datagetter
```

## Usage

### Basic Usage: `InitAndGet`

The most common use case is to ensure a piece of data is initialized before using it. `InitAndGet` is perfect for this. It takes a context and an initialization function. The first time it's called, it will run the function, store the result (including any errors), and all subsequent calls will return the stored result.

```go
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/juicymango/datagetter"
)

type Service struct {
	Data   string
	Getter datagetter.DataGetter
}

func (s *Service) InitializeData(ctx context.Context) error {
	fmt.Println("Initializing data...")
	time.Sleep(1 * time.Second)
	s.Data = "This is the initialized data"
	return nil
}

func main() {
	service := &Service{}
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			fmt.Printf("Goroutine %d trying to get data...\n", id)
			err := service.Getter.InitAndGet(context.Background(), service.InitializeData)
			if err != nil {
				fmt.Printf("Goroutine %d failed: %v\n", id, err)
				return
			}
			fmt.Printf("Goroutine %d got data: %s\n", id, service.Data)
		}(i)
	}

	wg.Wait()
}
```

### Asynchronous Initialization: `AsyncInit`

If the initialization is slow and you don't need the data immediately, you can start the initialization in the background with `AsyncInit`. Later, you can use `InitAndGet` with a `nil` initialization function to wait for the result.

```go
// In one part of your application
go service.Getter.AsyncInit(context.Background(), service.InitializeData)

// In another part of your application, where you need the data
err := service.Getter.InitAndGet(context.Background(), nil)
if err != nil {
    // Handle error
}
// Use service.Data
```

### Manual Control: `Start` and `Done`

For complex initialization logic that spans multiple functions, you can use `Start` and `Done` to manually control the state of the `DataGetter`.

```go
func DoSomething(getter *datagetter.DataGetter) {
    getter.Start()
    defer getter.Done()

    // ... complex logic ...
}
```

### Advanced Usage: `CallStackWait`

In some complex scenarios, a consumer goroutine might depend on data that is initialized within a complex call stack in another goroutine, and the initialization is not guaranteed to happen. `CallStackWait` is designed for this situation.

It allows a consumer to wait for a chain of `DataGetter`s. The wait will proceed down the chain as long as the next `DataGetter` in the chain is started. If a `DataGetter` in the chain is not started (because that path in the code was not taken), the wait will stop at the last completed `DataGetter` in the chain.

This prevents the consumer from waiting forever for something that will never happen.

```go
// In the producer goroutine
func A(getterA, getterB, getterC *datagetter.DataGetter) {
    getterA.Start()
    defer getterA.Done()

    B(getterB, getterC)
}

func B(getterB, getterC *datagetter.DataGetter) {
    getterB.Start()
    defer getterB.Done()

    if someCondition {
        C(getterC)
    }
}

func C(getterC *datagetter.DataGetter) {
    getterC.Start()
    defer getterC.Done()
    // ... initialize data ...
}

// In the consumer goroutine
func main() {
    var getterA, getterB, getterC datagetter.DataGetter
    
    go A(&getterA, &getterB, &getterC)

    // Wait for C, but if C is not called, wait for B to finish.
    // If B is not called, wait for A to finish.
    datagetter.CallStackWait(context.Background(), []*datagetter.DataGetter{&getterA, &getterB, &getterC})
    
    // Now it's safe to check the result of the initialization
}
```

## API Documentation

Full API documentation is available at [GoDoc](https://godoc.org/github.com/juicymango/datagetter).

## Coding Style and Philosophy

This package follows a specific coding style and philosophy:

*   **No private members:** All functions, structs, and constants are public.
*   **Freedom and Responsibility:** We provide examples for how to use this package, and we are responsible for ensuring those examples work correctly. We also give you the freedom to use the package in other ways, but in those cases, you are responsible for ensuring your usage is correct.

We believe this philosophy provides the best balance of guidance and flexibility.

## Contributing

We welcome contributions! Please see our [contributing guidelines](CONTRIBUTING.md) for more information.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.