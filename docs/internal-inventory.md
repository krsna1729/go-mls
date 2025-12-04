# Go-MLS Internal Package Inventory (Comprehensive)

**Generated**: December 4, 2025  
**Branch**: `main`  
**Status**: Interface cleanup + unified consumer lifecycle

---

## Executive Summary

This document provides a complete inventory of the `internal/` package tree:
- Public API surfaces
- Call relationships and dependencies  
- Concrete implementations
- Cleanup opportunities

**Package Count**: 7 (deleted: `internal/errors`)  
**Total Exports**: ~60 types, functions, and methods

---

## 1. Package Overview

```
internal/
├── api/           # HTTP routing (62 lines)
├── app/           # Dependency injection container
├── config/        # Configuration management
├── httputil/      # HTTP JSON utilities
├── logger/        # Logging abstraction
├── process/       # Process monitoring utilities
└── stream/        # Core streaming logic (27 files)
```

---

## 2. Package Details

### 2.1 internal/api

**Purpose**: HTTP route registration (thin wrapper)

#### Exported Types
```go
type Router struct {
    stream    *stream.StreamManager
    recording *stream.RecordingManager
    hls       *stream.HLSManager
    rtsp      *stream.RTSPServerManager
}
```

#### Exported Functions
```go
func NewRouter(appCtx *app.Context) *Router
func (rt *Router) RegisterRoutes(mux *http.ServeMux)
```

#### Dependencies
- `internal/app` (Context)
- `internal/stream` (All Api* functions)

#### Call Graph
```
main.go
  └→ api.NewRouter(appCtx)
       └→ router.RegisterRoutes(mux)
            └→ stream.ApiStartRelay(rt.stream)
            └→ stream.ApiStopRelay(rt.stream)
            └→ ... (all other Api* functions)
```

#### Notes
- **Cleanup Complete**: Removed 270 lines of duplicate handlers
- Now only registers `stream.Api*` functions
- Router struct could potentially be simplified further (holds 4 managers just to pass to Api functions)

---

### 2.2 internal/app

**Purpose**: Application context and dependency injection

#### Exported Types
```go
type Context struct {
    // Configuration
    Config *config.Config
    Logger *logger.Logger
    
    // Core managers
    Stream    *stream.StreamManager
    Recording *stream.RecordingManager
    HLS       *stream.HLSManager
    RTSP      *stream.RTSPServerManager
}
```

#### Exported Functions
```go
func NewContext(cfg *config.Config, log *logger.Logger) (*Context, error)
func (c *Context) Start() error
func (c *Context) Shutdown()
```

#### Initialization Sequence
```
NewContext()
  ├→ NewRTSPServerManager()
  ├→ NewInputRelayManager(rtsp)
  ├→ NewOutputRelayManager()
  ├→ NewStreamManager(input, output, rtsp)
  ├→ NewRecordingManager(streamManager as StreamProvider)
  ├→ NewHLSManager(streamManager as StreamProvider)
  └→ Wire cross-dependencies (SetCleanupHandler, etc.)
```

#### Dependency Graph
```
Context
  ├→ StreamManager
  │    ├→ InputRelayManager → RTSPServerManager
  │    ├→ OutputRelayManager
  │    └→ ConsumerRegistry
  ├→ RecordingManager → StreamManager (as StreamProvider)
  ├→ HLSManager → StreamManager (as StreamProvider)
  └→ RTSPServerManager (standalone)
```

---

### 2.3 internal/config

**Purpose**: Configuration loading and validation

#### Exported Types
```go
type Config struct {
    ServerHost      string
    ServerPort      int
    RTSPInterface   string
    RTSPPort        int
    RecordingsDir   string
    HLSDir          string
    FFmpegLogLevel  string
    // ... more fields
}
```

#### Exported Functions
```go
func DefaultConfig() *Config
func LoadConfig(filename string) (*Config, error)
func (c *Config) Validate() error
func (c *Config) GetRTSPServerURL() string
```

#### Usage
```
main.go → config.LoadConfig("config.json")
       → config.Validate()
       → pass to app.NewContext()
```

---

### 2.4 internal/httputil

**Purpose**: HTTP utilities for JSON handling

#### Exported Functions
```go
func WriteJSON(w http.ResponseWriter, status int, data interface{}) error
func WriteError(w http.ResponseWriter, status int, message string)
func DecodeJSON(r *http.Request, v interface{}) error  // Size-limited
```

#### Usage Pattern
```go
// All stream.Api* handlers use httputil
func ApiStartRelay(sm *StreamManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        httputil.DecodeJSON(r, &req)  // Decode with size limits
        // ...
        httputil.WriteJSON(w, http.StatusOK, response)
        // OR
        httputil.WriteError(w, http.StatusBadRequest, "error")
    }
}
```

#### Note
- Replaces old `errors.AppError` pattern (now deleted)
- Simpler, more standard approach

---

### 2.5 internal/logger

**Purpose**: Logging abstraction with rate limiting

#### Exported Types
```go
type Logger struct {
    // slog.Logger wrapper
}
```

#### Exported Functions
```go
func NewLogger(level string) *logger.Logger
func (l *Logger) Debug(msg string, args ...interface{})
func (l *Logger) Info(msg string, args ...interface{})
func (l *Logger) Warn(msg string, args ...interface{})
func (l *Logger) Error(msg string, args ...interface{})

// Rate-limited variants
func (l *Logger) DebugRateLimited(key, msg string, args ...interface{})
func (l *Logger) WarnRateLimited(key, msg string, args ...interface{})
```

#### Usage
- Every manager receives a logger in constructor
- Structured logging: `log.Info("msg", "key", value, "key2", value2)`

---

### 2.6 internal/process

**Purpose**: Process resource monitoring

#### Exported Functions
```go
func GetSelfUsage() (cpuPercent float64, memoryMB float64, err error)
func GetProcUsage(pid int) (cpuPercent float64, memoryMB float64, err error)
func GetChildrenUsage() (cpuPercent float64, memoryMB float64, err error)
```

#### Usage
```go
// In StreamManager.Status()
selfCPU, selfMem, _ := process.GetSelfUsage()
childCPU, childMem, _ := process.GetChildrenUsage()
```

---

### 2.7 internal/stream (Core Package)

**Lines of Code**: ~2500 lines across 27 files

#### 2.7.1 Interfaces

##### StreamProvider
```go
// File: interfaces.go
type StreamProvider interface {
    GetStream(inputName string) (url string, err error)
    ReleaseStream(inputName string)
}

// Implementation: InputRelayManager
```

**Used By**: RecordingManager, HLSManager

##### Consumer
```go
// File: consumer.go
type Consumer interface {
    Stop(inputName string) error
    OnFailure(handler ConsumerCleanupHandler) error
    GetConsumerType() string
    GetConsumerID() string
}

// Implementations:
//   - OutputRelayConsumer (output_relay_consumer.go)
//   - RecordingConsumer (recording_consumer.go)
//   - HLSConsumer (hls_consumer.go)
```

**Note**: `Start()` removed Dec 4, 2025 - was unused no-op

##### ConsumerCleanupHandler
```go
type ConsumerCleanupHandler interface {
    OnConsumerDone(inputURL, consumerID string, err error) error
}

// Implementation: StreamManager.OnConsumerDone()
// Change (Dec 4): Unified OnConsumerFailure + OnConsumerStopped
```

#### 2.7.2 Core Managers

##### StreamManager
```go
type StreamManager struct {
    InputRelays  *InputRelayManager
    OutputRelays *OutputRelayManager
    // ... more fields
}

// Public API:
func NewStreamManager(...) *StreamManager

// Stream Operations
func (sm *StreamManager) StartStream(inputURL, outputURL, inputName, outputName string, opts *FFmpegOptions, preset string) error
func (sm *StreamManager) StopStream(inputURL, outputURL, inputName, outputName string) error
func (sm *StreamManager) DeleteInput(inputURL, inputName string) error
func (sm *StreamManager) DeleteOutput(inputURL, outputURL, inputName, outputName string) error

// Consumer Management
func (sm *StreamManager) UnregisterConsumer(inputURL, consumerID string) bool
func (sm *StreamManager) OnConsumerFailure(inputURL, consumerID string) error

// Config/Status
func (sm *StreamManager) Status() StreamStatus
func (sm *StreamManager) ExportConfig(filename string) error
func (sm *StreamManager) ImportConfig(filename string) error
func (sm *StreamManager) GetEndpointConfig(inputURL, outputURL string) (string, *FFmpegOptions, error)

// Lifecycle
func (sm *StreamManager) Shutdown()
```

##### InputRelayManager
```go
type InputRelayManager struct {
    Relays map[string]*InputRelay
    // ...
}

// Public API:
func NewInputRelayManager(...) *InputRelayManager

// Input Operations
func (irm *InputRelayManager) StartInputRelay(inputURL, inputName string) (string, error)
func (irm *InputRelayManager) StopInputRelay(inputURL string) bool
func (irm *InputRelayManager) DeleteInput(inputURL string) error

// StreamProvider Implementation
func (irm *InputRelayManager) GetStream(inputName string) (string, error)
func (irm *InputRelayManager) ReleaseStream(inputName string)

// Refcount Management
func (irm *InputRelayManager) IncrementInputRef(inputURL, reason string)
func (irm *InputRelayManager) DecrementInputRef(inputURL, reason string)

// Status
func (irm *InputRelayManager) GetRelayStatus(inputURL string) (InputStatus, int, bool)
func (irm *InputRelayManager) GetRunningInputRelays() []*InputRelay
```

##### OutputRelayManager
```go
type OutputRelayManager struct {
    Relays map[string]*OutputRelay
    // ...
}

// Public API:
func NewOutputRelayManager(...) *OutputRelayManager

// Output Operations
func (orm *OutputRelayManager) StartOutputRelay(config OutputRelayConfig) error
func (orm *OutputRelayManager) StopOutputRelay(outputURL string)
func (orm *OutputRelayManager) DeleteOutput(outputURL string) error

// Consumer Pattern
func (orm *OutputRelayManager) SetCleanupHandler(handler ConsumerCleanupHandler)

// Status
func (orm *OutputRelayManager) GetRunningOutputRelays() []*OutputRelay
```

##### RecordingManager
```go
type RecordingManager struct {
    StreamProvider StreamProvider
    // ...
}

// Public API:
func NewRecordingManager(l *logger.Logger, recordingDir string, streamProvider StreamProvider, streamManager *StreamManager) *RecordingManager

// Recording Operations
func (rm *RecordingManager) StartRecording(name, source string) error
func (rm *RecordingManager) StopRecording(name string) error
func (rm *RecordingManager) GetRecordings() []RecordingInfo
func (rm *RecordingManager) DeleteRecording(filename string) error

// Lifecycle
func (rm *RecordingManager) Shutdown()
```

##### HLSManager
```go
type HLSManager struct {
    streamProvider StreamProvider
    streamManager  *StreamManager
    // ...
}

// Public API:
func NewHLSManager(l *logger.Logger, hlsDir string, streamProvider StreamProvider, streamManager *StreamManager) *HLSManager

// HLS Operations
func (m *HLSManager) CreateOrGetSession(inputName, inputURL string, viewerID string) (*HLSSession, error)
func (m *HLSManager) AddViewer(inputName string, viewerID string) error
func (m *HLSManager) RemoveViewer(inputName, viewerID string) error
func (m *HLSManager) UpdateHeartbeat(inputName, viewerID string) error
func (m *HLSManager) DeleteSession(inputName string)

// Session Info
func (m *HLSManager) GetSessions() map[string]*HLSSession
func (m *HLSManager) GetSession(inputName string) (*HLSSession, bool)
```

##### RTSPServerManager
```go
type RTSPServerManager struct {
    server *gortsplib.Server
    // ...
}

// Public API:
func NewRTSPServerManager(logger *logger.Logger, rtspInterface string, rtspPort int) (*RTSPServerManager, error)

// RTSP Operations
func (rsm *RTSPServerManager) Start() error
func (rsm *RTSPServerManager) AddStream(path string) (*gortsplib.ServerStream, error)
func (rsm *RTSPServerManager) RemoveStream(path string)
func (rsm *RTSPServerManager) GetStreamStats() map[string]StreamStats
func (rsm *RTSPServerManager) Close() error
```

#### 2.7.3 HTTP Handlers (relay_api.go)

All handlers follow this pattern:
```go
func Api{Action}(manager *Manager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // 1. Decode JSON
        httputil.DecodeJSON(r, &req)
        
        // 2. Validate
        if validation_fails {
            httputil.WriteError(w, status, msg)
            return
        }
        
        // 3. Call manager
        err := manager.{Action}(...)
        
        // 4. Respond
        if err != nil {
            httputil.WriteError(w, status, err.Error())
        } else {
            httputil.WriteJSON(w, http.StatusOK, response)
        }
    }
}
```

**List of API Handlers**:
- `ApiStartOutputRelay(sm *StreamManager)`
- `ApiStopOutputRelay(sm *StreamManager)` ← Updated Dec 4: now routes through StreamManager
- `ApiRelayStatus(sm *StreamManager)`
- `ApiExportRelays(sm *StreamManager)`
- `ApiImportRelays(sm *StreamManager)`
- `ApiRelayPresets()` (no args - static data)
- `ApiDeleteInput(sm *StreamManager)`
- `ApiDeleteOutput(sm *StreamManager)`
- `ApiRTSPStatus(rtsp *RTSPServerManager)`

**Recording Handlers**:
- `ApiStartRecording(rm *RecordingManager)`
- `ApiStopRecording(rm *RecordingManager)`
- `ApiListRecordings(rm *RecordingManager)`
- `ApiDeleteRecording(rm *RecordingManager)`
- `ApiDownloadRecording(rm *RecordingManager)`
- `ApiRecordingsSSE()` (SSE endpoint)

**HLS Handlers**:
- `ApiWatchInputHLS(hls *HLSManager, sm *StreamManager)`
- `ApiStartHLSViewer(hls *HLSManager, sm *StreamManager)`
- `ApiStopHLSViewer(hls *HLSManager, sm *StreamManager)`
- `ApiHLSViewerHeartbeat(hls *HLSManager)`

---

## 3. Call Relationship Diagrams

### 3.1 Application Startup

```
main()
  ├→ config.LoadConfig("config.json")
  ├→ logger.NewLogger(level)
  ├→ app.NewContext(cfg, log)
  │    ├→ stream.NewRTSPServerManager()
  │    ├→ stream.NewInputRelayManager(rtsp)
  │    ├→ stream.NewOutputRelayManager()
  │    ├→ stream.NewStreamManager(input, output, rtsp)
  │    ├→ stream.NewRecordingManager(streamMgr as StreamProvider)
  │    ├→ stream.NewHLSManager(streamMgr as StreamProvider)
  │    └→ outputMgr.SetCleanupHandler(streamMgr)
  ├→ appCtx.Start() → rtsp.Start()
  ├→ api.NewRouter(appCtx)
  ├→ router.RegisterRoutes(mux)
  └→ http.Server.ListenAndServe()
```

### 3.2 Start Stream Request Flow

```
HTTP POST /api/relay/start
  ↓
stream.ApiStartRelay(streamMgr)
  ├→ httputil.DecodeJSON(req)
  ├→ streamMgr.StartStream(inputURL, outputURL, inputName, outputName, opts, preset)
  │    ├→ inputMgr.GetStream(inputName)
  │    │    └→ If not running: StartInputRelay() → FFmpeg process
  │    ├→ applyPresetAndOptions() → FFmpegOptions
  │    ├→ outputMgr.StartOutputRelay(config)
  │    │    └→ Start FFmpeg: RTSP → RTMP
  │    ├→ NewOutputRelayConsumer(relay, outputMgr, inputURL)
  │    └→ consumerRegistry.Register(inputURL, consumer)
  └→ httputil.WriteJSON(response)
```

### 3.3 Consumer Failure Flow

```
FFmpeg Output Process Crashes
  ↓
outputMgr.monitorFFmpegProgress() detects exit
  ↓
outputMgr.cleanupOutputRelay(relay, reason)
  ├→ Check shuttingDown flag
  └→ If NOT shuttingDown:
       └→ relay.consumer.OnFailure(cleanupHandler)
            ├→ consumer.GetConsumerID()
            └→ cleanupHandler.OnConsumerFailure(inputURL, consumerID)
                 │ (This is streamMgr.OnConsumerFailure)
                 ├→ consumerRegistry.Unregister(inputURL, consumerID)
                 └→ inputMgr.DecrementInputRef(inputURL, reason)
                      └→ If refcount == 0:
                           └→ stopInputRelayAtZero() → Stop FFmpeg, keep in map
```

---

## 4. Cleanup Opportunities

### 4.1 Completed ✅
- Deleted `internal/errors` (153 lines) - completely unused
- Removed duplicate HTTP handlers from `router.go` (270 lines)
- Removed `sessionID` field from HLSConsumer
- Removed `logger` field from Router
- **Dec 4, 2025**: Removed `Consumer.Start()`, `ViewerManager` interface
- **Dec 4, 2025**: Unified `ConsumerCleanupHandler` to use `OnConsumerDone()`
- **Dec 4, 2025**: Fixed HLS to return 404 instead of dummy playlist
- **Dec 4, 2025**: Unified Start/Stop routing - both go through StreamManager

### 4.2 Remaining (From IDE Warnings)

#### Unused Functions in HLSManager
```go
// hls_manager.go
func (m *HLSManager) startInputRelayIfNeeded(...) error  // Line 286 - UNUSED
func (m *HLSManager) stopInputRelayIfNeeded(...) error   // Line 300 - UNUSED
func (m *HLSManager) getProcStopTimeout() time.Duration  // Line 822 - UNUSED
```

**Action**: Can be safely deleted (likely leftover from refactoring)

#### Unused Test Helper
```go
// hls_manager_test.go
func minimalHLSManagerConfig() HLSManagerConfig  // Line 187 - UNUSED
```

**Action**: Delete if not needed for future tests

### 4.3 Potential Simplifications (Not Urgent)

#### Router Struct
Current:
```go
type Router struct {
    stream    *stream.StreamManager
    recording *stream.RecordingManager  // Only passed to Api functions
    hls       *stream.HLSManager        // Only passed to Api functions
    rtsp      *stream.RTSPServerManager // Only passed to Api functions
}
```

Possible:
```go
type Router struct {
    stream *stream.StreamManager  // Can access all managers via this
}
```

**Decision**: Not recommended - would require broader refactoring

---

## 5. Test Coverage Overview

### 5.1 Test Files

| Package | Test Files | Coverage Focus |
|---------|-----------|---------------|
| `internal/config` | `config_test.go` | Config validation, defaults |
| `internal/httputil` | `httputil_test.go` | JSON encoding, size limits |
| `internal/stream` | 10+ test files | Managers, consumers, integration |
| `test/integration` | `fullstack_test.go` | End-to-end workflows |

### 5.2 Key Test Patterns

**Unit Tests**:
```go
// Test with mocks
mgr := NewHLSManager(log, tmpDir, &mockStreamProvider{}, nil)
```

**Integration Tests**:
```go
// Test with real components
setupFullStackTestEnv(t) → Real StreamManager, RTSP, FFmpeg
```

**Consumer Tests**:
```go
// Test consumer pattern
consumer := NewOutputRelayConsumer(relay, orm, inputURL)
consumer.OnFailure(&mockCleanupHandler{})
```

---

## 6. Dependency Summary Table

| Package | Depends On | Used By |
|---------|-----------|---------|
| `api` | `app`, `stream` | `main` |
| `app` | `config`, `logger`, `stream` | `main`, `api` |
| `config` | (stdlib only) | `main`, `app` |
| `httputil` | (stdlib only) | `stream`(Api handlers) |
| `logger` | (stdlib,slog) | All managers |
| `process` | (stdlib,procfs) | `stream`(StreamManager) |
| `stream` | `logger`, `httputil`, `process` | `app`, `api` |

**Circular Dependencies**: None ✅ (broken by interfaces)

---

## 7. Interface Usage Map

| Interface | Implemented By | Consumed By | Purpose |
|-----------|---------------|-------------|---------|
| `StreamProvider` | `InputRelayManager` | `RecordingManager`, `HLSManager` | Abstract input acquisition |
| `Consumer` | `OutputRelayConsumer`, `RecordingConsumer`, `HLSConsumer` | `StreamManager` (via ConsumerRegistry) | Track consumers for refcounting |
| `ConsumerCleanupHandler` | `StreamManager` | All Consumer implementations | Failure callback DI |

---

## 8. Export Inventory (Full List)

### Managers (7)
- StreamManager
- InputRelayManager
- OutputRelayManager
- RecordingManager
- HLSManager
- RTSPServerManager
- ViewerManager (embedded in HLS)

### Interfaces (3)
- StreamProvider
- Consumer (Start() removed)
- ConsumerCleanupHandler (unified to OnConsumerDone)

### Consumer Implementations (3)
- OutputRelayConsumer
- RecordingConsumer
- HLSConsumer

### HTTP Handlers (18 Api* functions)
- Relay: Start, Stop, Status, Export, Import, Presets, DeleteInput, DeleteOutput
- RTSP: Status
- Recording: Start, Stop, List, Delete, Download, SSE
- HLS: WatchInput, StartViewer, StopViewer, Heartbeat

### Configuration Types
- Config, FFmpegOptions, OutputRelayConfig, etc.

### Status Types
- StreamStatus, InputStatus, OutputStatus, RecordingInfo, HLSSession

---

## 9. Unused Code Summary

**Safe to Delete**:
1. `hls_manager.go:startInputRelayIfNeeded()` (line 286)
2. `hls_manager.go:stopInputRelayIfNeeded()` (line 300)
3. `hls_manager.go:getProcStopTimeout()` (line 822)
4. `hls_manager_test.go:minimalHLSManagerConfig()` (line 187)

**Estimated Savings**: ~50-80 lines

---

## 10. Recommendations

### High Priority
1. ✅ **Delete unused HLS methods** (4 functions)
2. ✅ **Already deleted**: `internal/errors`, duplicate handlers

### Low Priority
3. Consider consolidating Router fields (architectural decision)

### Not Recommended
- Merging StreamProvider and Consumer (serve different purposes)
- Moving HTTP handlers from `stream` to `api` (they're close to business logic)

---

**End of Inventory**

**Total Packages**: 7  
**Total Interfaces**: 3  
**Total Managers**: 7  
**Total HTTP Handlers**: 18  
**Architecture Quality**: ✅ Clean, well-organized, minimal coupling
