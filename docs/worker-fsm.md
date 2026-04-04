# Worker Lifecycle FSM

## Worker Interface

```go
type Worker interface {
    Start(ctx context.Context) error
    Stop()
    Wait() error
    State() WorkerState
    Done() <-chan struct{}
}
```

## Worker States

| State | Description |
|-------|-------------|
| `Stopped` | Initial state, worker not started |
| `Starting` | Worker is initializing |
| `Running` | Worker is active |
| `Stopping` | Worker is shutting down |
| `Error` | Worker encountered an error |

## BaseWorker

Provides common lifecycle management for all workers:

```go
type BaseWorker struct {
    name   string
    log    *logger.Logger
    mu     sync.RWMutex
    state  WorkerState
    stopCh chan struct{}
    doneCh chan struct{}
    exitErr error
    goroutineWG sync.WaitGroup
}
```

### Key Methods

- `Start()` - Transitions to `Starting`, resets channels, returns immediately
- `Stop()` - Non-blocking, sends signal on `stopCh`
- `Wait()` - Blocks until `doneCh` is closed
- `Done()` - Returns `doneCh` for external observation
- `State()` - Returns current state (thread-safe)
- `Shutdown()` - Calls `Stop()` then blocks on `goroutineWG`

## ProcessWorker

Extends `BaseWorker` with process management:

```go
type ProcessWorker struct {
    *BaseWorker
    procMu sync.Mutex
    proc   Process
}
```

### Process Interface

```go
type Process interface {
    PID() int
    Stop()
    Wait() error
    Done() <-chan struct{}
    Err() error
}
```

### Lifecycle

1. `RunProcessWorker()` creates worker and starts goroutine
2. Goroutine runs factory to create process
3. Process is assigned (thread-safe via `procMu`)
4. Select on `stopCh` or `proc.Done()`
5. On exit: cleanup process, call `complete()`
6. `goroutineWG.Done()` ensures cleanup before return

```go
func RunProcessWorker(name string, log *logger.Logger, factory ProcessFactory) (*ProcessWorker, error) {
    w := NewProcessWorker(name, log)
    if err := w.Start(); err != nil {
        return nil, err
    }

    w.goroutineWG.Add(1)
    go func() {
        defer func() {
            w.goroutineWG.Done()
            w.complete(w.exitErr)
        }()

        proc, err := factory(context.Background())
        if err != nil {
            w.exitErr = err
            w.setState(WorkerStateStopping)
            return
        }

        w.procMu.Lock()
        w.proc = proc
        w.procMu.Unlock()
        w.setState(WorkerStateRunning)

        select {
        case <-w.stopCh:
            w.setState(WorkerStateStopping)
            proc.Stop()
        case <-proc.Done():
            if err := proc.Err(); err != nil {
                w.exitErr = fmt.Errorf("%w: %v", ErrProcessFailed, err)
            }
            w.setState(WorkerStateStopping)
        }
    }()

    return w, nil
}
```

## Typed Errors

Located in `internal/worker/errors.go`:

```go
var (
    ErrProcessFailed = errors.New("process failed")
    ErrProcessKilled = errors.New("process killed")
)
```

### Helper Functions

- `IsProcessKilled(err)` - Checks if error is from SIGKILL/SIGTERM
- `IsProcessFailed(err)` - Checks if process exited with non-zero code
- `GetExitCode(err)` - Returns exit code from `*exec.ExitError`
- `IsSignaled(err)` - Checks if process was killed by signal
- `NormalizeError(err)` - Converts raw errors to typed errors

## State Diagram

```mermaid
stateDiagram-v2
    [*] --> Stopped

    Stopped --> Starting: Start()
    Starting --> Running: Process created
    Starting --> Stopping: Factory fails

    Running --> Stopping: Stop() or Process exits

    Stopping --> Stopped: complete()
    Stopping --> Error: complete(err)

    Error --> [*]
    Stopped --> [*]: Shutdown
```

## Shutdown Order

For graceful shutdown, components must stop in order:

1. **HTTP Server** - Stop accepting new requests
2. **Workers** - Call `Shutdown()` on each worker
3. **App Context** - Stop Hub, Ingest, HLSManager

```go
// Shutdown sequence
server.Shutdown()       // 1. HTTP server
appCtx.Shutdown()       // 2. Stops Hub, Ingest, HLSManager
<-ctx.Done()            // 3. Wait for context cancellation
```

## Data Race Prevention

The `ProcessWorker` uses `procMu` mutex to protect `proc` field:

- `Stop()` locks before checking `w.proc`
- Factory goroutine locks before setting `w.proc`
- Prevents races between `Stop()` and goroutine assignment
