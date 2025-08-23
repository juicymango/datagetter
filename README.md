# DataGetter

A robust Go package for managing concurrent data initialization with one-time execution, error handling, and context support.

## Features

- **One-time Initialization**: Ensures initialization functions run only once, even with concurrent access
- **Error Handling**: Proper error propagation from initialization functions
- **Context Support**: Full context cancellation and timeout support
- **Panic Recovery**: Built-in panic handling with customizable recovery
- **Dependency Management**: `CallStackWait` for managing initialization dependencies
- **Thread Safety**: Fully concurrent-safe implementation

## Installation

```bash
go get github.com/juicymango/datagetter
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"time"
	
	"github.com/juicymango/datagetter"
)

type Service struct {
	data   string
	getter *datagetter.DataGetter
}

func (s *Service) initialize(ctx context.Context) error {
	// Simulate expensive initialization
	select {
	case <-time.After(2 * time.Second):
		s.data = "initialized data"
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	service := &Service{
		getter: datagetter.NewDataGetter(),
	}
	
	// Multiple goroutines can safely call InitAndGet
	ctx := context.Background()
	err := service.getter.InitAndGet(ctx, service.initialize)
	if err != nil {
		panic(err)
	}
	
	fmt.Println("Data:", service.data)
}
```

## Core Concepts

### DataGetter

The main type that manages data initialization. It ensures that:
- Initialization happens only once
- Errors are properly propagated
- Context cancellation is respected
- Panics are recovered and converted to errors

### InitFunc

Initialization functions must have the signature:
```go
type InitFunc func(ctx context.Context) error
```

They receive a context for cancellation/timeout support and return an error.

### PanicHandler

Custom panic handlers can be provided:
```go
type PanicHandler func(p any) error
```

## Usage Examples

### Basic Usage

```go
getter := NewDataGetter()
err := getter.InitAndGet(ctx, func(ctx context.Context) error {
	// Your initialization logic here
	return nil
})
```

### With Custom Panic Handling

```go
handler := func(p any) error {
	return fmt.Errorf("initialization panic: %v", p)
}
getter := NewDataGetter(handler)
```

### Asynchronous Initialization

```go
getter.AsyncInit(ctx, func(ctx context.Context) error {
	// This runs in a separate goroutine
	return nil
})
// Later...
err := getter.InitAndGet(ctx, nil)
```

### Using Start/Done for Multi-function Initialization

```go
func complexInit(getter *DataGetter) {
	getter.Start()
	defer getter.Done()
	
	// Multiple initialization steps across functions
	step1()
	step2()
}
```

### Dependency Management with CallStackWait

```go
// Wait for a chain of dependencies
path := []*DataGetter{getterA, getterB, getterC}
datagetter.CallStackWait(ctx, path)
```

## Code Style Philosophy

This package follows a unique code style philosophy:

### No Private Functions or Variables

All functions, structs, and constants are exported. This design choice reflects the philosophy that:

1. **Example-Driven Usage**: The package provides clear examples and documented usage patterns. Users are expected to follow these patterns for common scenarios.

2. **Maximum Freedom**: For advanced or unconventional use cases, all internals are exposed. Users have the freedom to use the package in ways not explicitly documented, but they assume responsibility for understanding the consequences.

3. **Transparency Over Obscurity**: Rather than hiding implementation details behind private APIs, everything is exposed. This encourages users to understand how the package works and make informed decisions about their usage.

4. **Responsibility Sharing**: The package author provides well-tested examples and common patterns. Users who deviate from these patterns take on the responsibility for testing and maintaining their specific usage.

This approach balances ease of use for common cases with maximum flexibility for advanced users.

## API Reference

### func NewDataGetter

```go
func NewDataGetter(handler ...PanicHandler) *DataGetter
```

Creates a new DataGetter instance. Optionally takes a custom panic handler.

### func (*DataGetter) InitAndGet

```go
func (d *DataGetter) InitAndGet(ctx context.Context, initFunc InitFunc) error
```

Initializes and waits for completion. Returns any error from initialization.

### func (*DataGetter) AsyncInit

```go
func (d *DataGetter) AsyncInit(ctx context.Context, initFunc InitFunc)
```

Starts asynchronous initialization in a new goroutine.

### func (*DataGetter) Start

```go
func (d *DataGetter) Start()
```

Marks the beginning of a multi-step initialization process.

### func (*DataGetter) Done

```go
func (d *DataGetter) Done()
```

Marks the end of a multi-step initialization process.

### func CallStackWait

```go
func CallStackWait(ctx context.Context, path []*DataGetter)
```

Waits for a chain of DataGetter dependencies to complete.

## Error Handling

DataGetter converts all initialization failures (including panics) into errors:

- `InitFunc` return values are preserved
- Panics are converted to descriptive errors
- Context cancellations return `context.Canceled` or `context.DeadlineExceeded`

## Performance

- Zero allocations on the fast path (when already initialized)
- Minimal locking using atomic operations
- Efficient channel-based synchronization

## License

Apache 2.0 - See LICENSE file for details.

## Contributing

Contributions are welcome! Please ensure:

1. All code is thoroughly tested
2. Documentation is updated
3. The public API remains stable
4. New features include comprehensive examples