# Go-MLS Architecture Overview (Comprehensive)

**Generated**: December 4, 2025  
**Branch**: `main`  
**Status**: Interface cleanup complete, unified consumer lifecycle

---

## Executive Summary

Go-MLS is a streaming media system that:
- Accepts input streams (RTMP/RTSP/HTTP/File)
- Transcodes them via FFmpeg to local RTSP
- Distributes to outputs (RTMP), recordings (MP4), and HLS viewers
- Manages reference counting to optimize resource usage

**Architecture Style**: Layered with interface-driven dependency injection  
**Key Patterns**: Consumer pattern for refcount management, StreamProvider for input abstraction

---

## 1. High-Level Component Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│                          main.go                                  │
│                  (Application Entry Point)                        │
└───────────────────────────┬──────────────────────────────────────┘
                            │
                            ↓
                ┌───────────────────────┐
                │   app.Context         │  ← Dependency Injection Root
                │   (Wiring & Lifecycle)│
                └───────────┬───────────┘
                            │
        ┌───────────────────┼───────────────────┬─────────────┐
        │                   │                   │             │
        ↓                   ↓                   ↓             ↓
┌───────────────┐   ┌──────────────┐   ┌──────────────┐   ┌────────────┐
│ api.Router    │   │StreamManager │   │RecordingMgr  │   │ HLSManager │
│ (HTTP Routes) │   │ (Core Logic) │   │ (MP4 Files)  │   │ (HLS/Web)  │
└───────┬───────┘   └──────┬───────┘   └──────┬───────┘   └─────┬──────┘
        │                  │                  │                  │
        │                  ↓                  │                  │
        │          ┌───────────────┐          │                  │
        │          │InputRelayMgr  │←─────────┴──────────────────┘
        │          │(Input Streams)│      StreamProvider Interface
        │          └───────┬───────┘
        │                  │
        │                  ↓
        │          ┌───────────────┐
        │          │OutputRelayMgr │
        │          │(Output Streams)│
        │          └───────────────┘
        │                  │
        └──────────────────┴────────→ Uses stream.Api* HTTP Handlers
```

---

## 2. Component Responsibilities

### 2.1 Core Managers

| Component | Responsibility | Dependencies |
|-----------|---------------|--------------|
| **main.go** | App bootstrap, signal handling, resource monitoring | app.Context |
| **app.Context** | DI container, wire all components, lifecycle mgmt | All managers |
| **api.Router** | HTTP route registration | StreamManager, RecordingMgr, HLSMgr, RTSPMgr |
| **StreamManager** | Orchestrate inputs/outputs, consumer registry | InputRelayMgr, OutputRelayMgr, Consumer* |
| **InputRelayManager** | Manage input FFmpeg → RTSP, refcounting | RTSPServerManager |
| **OutputRelayManager** | Manage output RTSP → RTMP FFmpeg | ConsumerCleanupHandler |
| **RecordingManager** | Manage MP4 recordings from RTSP | StreamProvider |
| **HLSManager** | Manage HLS sessions, viewer tracking | StreamProvider |
| **RTSPServerManager** | Embed RTSP server | None (standalone) |

### 2.2 Interface Roles

#### StreamProvider Interface
**Purpose**: Decouple consumers (Recording, HLS) from `InputRelayManager`  
**Pattern**: Dependency Inversion Principle

```go
type StreamProvider interface {
    GetStream(inputName string) (rtspURL string, error)
    ReleaseStream(inputName string)
}
```

**Implementation**: `InputRelayManager`  
**Consumers**: `RecordingManager`, `HLSManager`

**Why needed**:
- Recordings/HLS don't need to know about input relay internals
- Can mock for testing
- Follows "program to interface, not implementation"

#### Consumer Interface  
**Purpose**: Unified refcount management and failure handling  
**Pattern**: Observer + Strategy pattern

```go
type Consumer interface {
    Stop(inputName string) error
    OnFailure(handler ConsumerCleanupHandler) error
    GetConsumerType() string
    GetConsumerID() string
}
```

**Note**: `Start()` method removed (Dec 4, 2025) - consumers are started upon creation, not via interface

**Implementations**:
- `OutputRelayConsumer` (output FFmpeg processes)
- `RecordingConsumer` (MP4 recordings)
- `HLSConsumer` (HLS sessions)

**Why needed**:
- Automatic refcount decrement when consumers stop/fail
- Polymorphic cleanup (StreamManager doesn't need to know consumer type)
- Failure propagation with dependency injection

#### ConsumerCleanupHandler
**Purpose**: Callback for consumer failure without circular dependencies

```go
type ConsumerCleanupHandler interface {
    OnConsumerDone(inputURL, consumerID string, err error) error
}
```

**Implementation**: `StreamManager.OnConsumerDone()`

**Change** (Dec 4, 2025): Unified `OnConsumerFailure` and `OnConsumerStopped` into single `OnConsumerDone` method. 
- `err == nil`: Graceful stop
- `err != nil`: Failure

**Pattern**: Dependency Injection (consumers receive handler, not store it)

---

## 3. Data Flow Diagrams

### 3.1 Start Output Relay Flow

```
User/API Request
     │
     ↓
ApiStartRelay (HTTP Handler)
     │
     ├──→ Decode JSON (inputName, outputName, preset, options)
     │
     ↓
StreamManager.StartStream()
     │
     ├──→ InputRelayManager.GetStream(inputName)
     │         │
     │         ├──→ Check if input already running (refcount > 0)
     │         │    YES: Return existing RTSP URL, increment refcount
     │         │    NO:  Start new FFmpeg input → RTSP, set refcount=1
     │         │
     │         └──→ Return: rtsp://localhost:8554/relay/{inputName}
     │
     ├──→ applyPresetAndOptions(preset, options) → FFmpegOptions
     │
     ├──→ OutputRelayManager.StartOutputRelay(localURL, outputURL, opts)
     │         │
     │         ├──→ Start FFmpeg: RTSP → RTMP with transcoding
     │         │
     │         └──→ Create OutputRelayConsumer
     │
     └──→ ConsumerRegistry.Register(inputURL, consumer)
           (This is the "official" refcount increment)

Result: Input refcount++, output running, consumer tracked
```

### 3.2 Stop Output Relay Flow

```
User/API Request
     │
     ↓
ApiStopOutputRelay (HTTP Handler)
     │
     ↓
StreamManager.StopStream()
     │
     ├──→ StreamManager.UnregisterConsumer(inputURL, outputURL)
     │         │
     │         ├──→ ConsumerRegistry.Unregister(inputURL, outputURL)
     │         │
     │         └──→ InputRelayManager.DecrementInputRef(inputURL, reason)
     │                   │
     │                   ├─→ Decrement refcount
     │                   │
     │                   └─→ If refcount == 0:
     │                         └→ stopInputRelayAtZero()
     │                              ├→ Stop FFmpeg process
     │                              ├→ Set status = InputStopped
     │                              ├→ Clean up RTSP stream
     │                              └→ Keep relay in map (preserves state)
     │
     └──→ OutputRelayManager.StopOutputRelay(outputURL)
            └──→ Stop output FFmpeg process

Result: Input refcount--, output stopped, consumer removed
Note: Symmetric with Start - both route through StreamManager
```

### 3.3 Consumer Failure Flow (Output FFmpeg crashes)

```
Output FFmpeg Process
     │
     ├──→ Exit with error
     │
     ↓
OutputRelayManager.cleanupOutputRelay()
     │
     ├──→ Check: shuttingDown? 
     │    YES: Log "Graceful shutdown, not calling consumer cleanup"
     │    NO:  Continue ↓
     │
     └──→ consumer.OnFailure(cleanupHandler)
           │
           ├──→ consumer.GetConsumerID() → "output-rtmp://..."
           │
           └──→ cleanupHandler.OnConsumerFailure(inputURL, consumerID)
                 │
                 │    (This is StreamManager.OnConsumerFailure)
                 │
                 ├──→ ConsumerRegistry.Unregister(inputURL, consumerID)
                 │
                 └──→ InputRelayManager.DecrementInputRef(inputURL, reason)
                       └→ Refcount decremented!

Result: Failed consumer auto-cleaned, refcount properly managed
```

---

## 4. Interface Analysis: StreamProvider vs Consumer

### Are Both Interfaces Needed? **YES**

They serve **different purposes** and operate at **different levels**:

| Aspect | StreamProvider | Consumer |
|--------|---------------|----------|
| **Purpose** | Abstract input stream acquisition | Track/manage consumers for refcounting |
| **Direction** | Consumer → Input (pull model) | Input → Consumer (notify model) |
| **Lifecycle** | Get stream / Release stream | Start / Stop / OnFailure |
| **Used By** | RecordingMgr, HLSMgr | OutputRelays, Recordings, HLS |
| **Registered In** | N/A (just method calls) | ConsumerRegistry (tracked) |
| **Refcount** | Managed by GetStream/Release | Managed by Register/Unregister |
| **Level** | High-level (abstracts input source) | Low-level (refcount participant) |

### Example Scenarios

**StreamProvider**:
```go
// Recording wants an input stream
localURL, err := rm.StreamProvider.GetStream("English")
// → InputRelayManager starts FFmpeg if needed, returns RTSP URL
```

**Consumer**:
```go
// Output relay crashes
outputRelay.consumer.OnFailure(cleanupHandler)
// → StreamManager unregisters consumer, decrements refcount
```

**Interaction**:
- `RecordingManager` uses **StreamProvider** to get streams
- `RecordingConsumer` implements **Consumer** to track lifecycle
- Both are needed because one is for **acquisition**, other for **tracking**

---

## 5. HTTP API Layer

### 5.1 Handler Pattern

**Before Cleanup**: Duplicate handlers in `router.go` and `relay_api.go`  
**After Cleanup**: Single canonical implementation in `stream/relay_api.go`

```go
// router.go (thin wrapper, just registration)
func (rt *Router) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("/api/relay/start", stream.ApiStartRelay(rt.stream))
    mux.HandleFunc("/api/relay/stop", stream.ApiStopRelay(rt.stream))
    // ... all handlers use stream.Api* functions
}
```

**Benefits**:
- Single source of truth
- Consistent error handling (`httputil.WriteError`)
- Less code (eliminated 270 lines)

### 5.2 API Endpoints

| Endpoint | Handler | Manager Called |
|----------|---------|----------------|
| `POST /api/relay/start` | ApiStartRelay | StreamManager.StartStream() |
| `POST /api/relay/stop` | ApiStopRelay | StreamManager.StopStream() |
| `GET /api/relay/status` | ApiRelayStatus | StreamManager.Status() |
| `POST /api/relay/delete-input` | ApiDeleteInput | StreamManager.DeleteInput() |
| `POST /api/relay/delete-output` | ApiDeleteOutput | StreamManager.DeleteOutput() |
| `GET /api/relay/presets` | ApiRelayPresets | (static data) |
| `POST /api/relay/export` | ApiExportRelays | StreamManager.ExportConfig() |
| `POST /api/relay/import` | ApiImportRelays | StreamManager.ImportConfig() |
| `GET /api/rtsp/status` | ApiRTSPStatus | RTSPServer.GetStreamStats() |
| `POST /api/recording/*` | ApiStart/Stop/ListRecording | RecordingManager.* |
| `GET /api/relay/watch-input/hls/*` | ApiWatchInputHLS | HLSManager.* |

---

## 6. Refcount Management

### 6.1 Refcount States

```
Input Relay States:
┌──────────────┐
│ Not Created  │ (refcount = N/A)
└──────┬───────┘
       │ First consumer requests
       ↓
┌──────────────┐
│ Running      │ (refcount >= 1)
│ FFmpeg→RTSP  │
└──────┬───────┘
       │ Last consumer stops
       ↓
┌──────────────┐
│ Stopped      │ (refcount = 0, kept in map)
│ Status=Stop  │
└──────┬───────┘
       │ Explicit DeleteInput API
       ↓
┌──────────────┐
│ Deleted      │ (removed from map)
└──────────────┘
```

### 6.2 Refcount Operations

| Operation | Effect on Refcount | Trigger |
|-----------|-------------------|---------|
| `StartStream()` | +1 | Output relay starts |
| `StopStream()` | -1 | Output relay stops |
| `StartRecording()` | +1 | Recording starts |
| `StopRecording()` | -1 | Recording Stop |
| `AddHLSViewer()` | +1 (first viewer) | HLS session created |
| `RemoveHLSViewer()` | -1 (last viewer) | HLS session timeout |
| Consumer failure | -1 | FFmpeg crash, handled via OnFailure() |

**Critical**: `DeleteInput()` does NOT decrement refcount - it forcefully removes input regardless

---

## 7. Testing Architecture

### 7.1 Test Layers

| Test Type | Location | Purpose |
|-----------|----------|---------|
| Unit Tests | `internal/stream/*_test.go` | Individual manager logic |
| Integration Tests | `test/integration/fullstack_test.go` | End-to-end workflows |
| API Tests | `internal/stream/*_api_test.go` | HTTP handler behavior |

### 7.2 Key Integration Tests

- `TestFullStack_ConcurrentConsumers`: Refcount 7→2→1→0 lifecycle
- `TestFullStack_MultiConsumerLifecycle`: Sequential consumer management  
- `TestPresetConfigImport`: FFmpeg preset validation

**Total**: 98 tests passing ✅

---

## 8. Recent Architectural Improvements

### 8.1 Cleanup Summary (Dec 2, 2025)

| Item | Lines Removed | Impact |
|------|--------------|--------|
| Duplicate HTTP handlers | ~270 | 81% reduction in router.go |
| `internal/errors` package | 153 | Removed entire unused package |
| Unused fields (`sessionID`, `logger`) | ~5 | Minor cleanup |
| **Total** | **~428 lines | **Codebase cleanup**

### 8.2 Consumer Pattern Benefits

**Before**: Manual refcount management, prone to leaks  
**After**: Automatic via Consumer interface

**Example**:
```go
// OLD (error-prone)
sm.consumerRegistry.Unregister(...)  // Forgot to decrement!

// NEW (automatic)
sm.UnregisterConsumer(...)  // Does both: unregister + decrement
```

### 8.3 Interface Cleanup (Dec 4, 2025)

| Change | Reason | Impact |
|--------|--------|--------|
| Removed `Consumer.Start()` | Method was unused (no-op in all implementations) | Simpler interface |
| Removed `ViewerManager` interface | Only one implementation, tightly coupled | Less indirection |
| Unified `OnConsumerDone()` | Replaced `OnConsumerFailure` + `OnConsumerStopped` | Consistent cleanup API |
| HLS 404 on missing session | Was serving dummy playlist | Proper error semantics |
| Unified Start/Stop routing | Both now route through StreamManager | Consistent API pattern |

---

## 9. Design Principles Followed

1. **Interface Segregation**: Small, focused interfaces (StreamProvider, Consumer)
2. **Dependency Inversion**: Depend on abstractions, not concretions
3. **Single Responsibility**: Each manager has one clear job
4. **Dependency Injection**: Cleanup handlers passed, not stored
5. **Fail-Safe Defaults**: Refcount=0 stops (not deletes) inputs

---

## 10. Future Architecture Opportunities

From `architecture_notes.md`:

1. **Move StopStream logic to OutputRelayManager** (polymorphic cleanup)
2. **DeleteInput broadcasts to consumers** (graceful notification)
3. **Status aggregation via interface** (StatusProvider pattern)
4. **Shutdown via LifecycleManager** (polymorphic shutdown)

**Not Urgent**: Current architecture is clean and stable.

---

## Appendix: Package Structure

```
go-mls/
├── cmd/
│   └── server/main.go          # Entry point
├── internal/
│   ├── api/
│   │   └── router.go           # HTTP route registration (62 lines)
│   ├── app/
│   │   └── context.go          # DI container
│   ├── config/
│   │   └── config.go           # Configuration
│   ├── httputil/
│   │   └── json.go             # HTTP utilities
│   ├── logger/
│   │   └── logger.go           # Logging abstraction
│   ├── process/
│   │   └── process.go          # Process utilities
│   └── stream/
│       ├── interfaces.go       # StreamProvider
│       ├── consumer.go         # Consumer, ConsumerRegistry
│       ├── *_consumer.go       # Consumer implementations
│       ├── *_manager.go        # Manager implementations
│       └── *_api.go            # HTTP handlers
└── test/
    └── integration/
        └── fullstack_test.go   # Integration tests
```

**Note**: `internal/errors/` has been deleted (unused since cleanup).

---

**End of Architecture Overview**
