# Go-MLS Architecture

**Generated**: April 2026  
**Status**: Current Architecture

---

## Executive Summary

Go-MLS is a streaming media gateway that:
- Accepts input streams (RTSP, RTMP, HTTP, File)
- Converts them to local RTSP via FFmpeg
- Distributes to multiple outputs, recordings, and HLS viewers
- Uses reference counting to optimize resource usage

The architecture uses a single **Pipeline** struct that manages all streams with direct method calls—no interfaces, no callbacks, no global state.

---

## Component Overview

```mermaid
flowchart TB
    subgraph main["main.go"]
        A["Entry Point"]
    end
    
    subgraph app["app.Context"]
        B["Pipeline"]
        C["RTSPServerManager"]
    end
    
    subgraph pipeline["Pipeline"]
        D["inputs<br/>map[name]*PipelineStream"]
        E["outputs<br/>map[name]*PipelineStream"]
        F["recordings<br/>map[key]*PipelineRecording"]
        G["hlsSessions<br/>map[name]*PipelineHLSSession"]
    end
    
    subgraph external["Injected Dependencies"]
        H["FFmpegFactory<br/>(interface)"]
        I["SSEBroker<br/>(concrete)"]
    end
    
    subgraph rtsp["RTSP Server"]
        J["gortsplib.Server"]
    end
    
    A --> B
    A --> C
    B --> D
    B --> E
    B --> F
    B --> G
    B --> H
    B --> I
    C --> J
```

---

## Dependency Injection

```mermaid
flowchart LR
    subgraph init["Initialization"]
        direction TB
        A["config.Load()"] --> B["app.NewContext()"]
        B --> C["stream.NewPipeline()"]
        C --> D["pipeline.SetRTSPServer()"]
    end
    
    subgraph handlers["HTTP Handlers"]
        direction TB
        E["api/router.go"] --> F["stream.ApiStartOutputRelay(pipeline)"]
        F --> G["pipeline.StartOutput()"]
    end
```

---

## Start Output Relay Flow

```mermaid
sequenceDiagram
    participant Client
    participant API as "relay_api.go"
    participant Pipeline
    participant FFmpeg as "FFmpegFactory"
    participant RTSP as "RTSPServer"
    
    Client->>API: POST /api/relay/start<br/>{input_name, output_name, ...}
    
    API->>Pipeline: StartInput(ctx, inputName, inputURL)
    Note over Pipeline: Check if input URL already running
    
    alt Input not running
        Pipeline->>RTSP: GetRTSPURL(relayPath)
        RTSP-->>Pipeline: rtsp://localhost:8554/relay/{name}
        Pipeline->>FFmpeg: NewInputProcess(src, dst)
        FFmpeg-->>Pipeline: FFmpegProcess
        Pipeline->>FFmpeg: Start()
        Pipeline->>RTSP: WaitForStreamReady()
    end
    
    API->>Pipeline: StartOutput(ctx, outputName, inputName, destURL, opts)
    
    alt Output not running
        Pipeline->>FFmpeg: NewOutputProcess(src, dst, opts)
        FFmpeg-->>Pipeline: FFmpegProcess
        Pipeline->>FFmpeg: Start()
    end
    
    Pipeline-->>Client: 200 OK<br/>{status: "started"}
```

---

## Input Lifecycle (Reference Counting)

```mermaid
stateDiagram-v2
    [*] --> NotRunning: No consumers
    
    state NotRunning {
        [*] --> InputRequested: Consumer wants input
        InputRequested --> Starting: FFmpeg process spawns
        Starting --> Running: RTSP stream ready
        Running --> Starting: Restart on failure
    }
    
    state Running {
        [*] --> Active: RefCount > 0
        Active --> Active: AddConsumer<br/>RefCount++
        Active --> Active: RemoveConsumer<br/>RefCount--
        Active --> Stopping: RefCount == 0
        Stopping --> [*]: FFmpeg stops
    }
    
    Running --> [*]: DeleteInput API
```

### Refcount Operations

| Operation | RefCount Change | Trigger |
|-----------|----------------|---------|
| StartOutput | +1 | Output relay starts |
| StopOutput | -1 | Output relay stops |
| StartRecording | +1 | Recording starts |
| StopRecording | -1 | Recording stops |
| StartHLSViewer | +1 | First viewer joins |
| StopHLSViewer | -1 | Last viewer leaves |

---

## Recording Flow

```mermaid
flowchart TB
    subgraph start["Start Recording"]
        A["POST /api/recording/start"] --> B["Get input URL"]
        B --> C{"Input exists?"}
        C -->|No| D["StartInput()"]
        C -->|Yes| E["Use existing input"]
        D --> F["StartRecording()"]
        E --> F
        F --> G["Increment input RefCount"]
        G --> H["Create FFmpeg process"]
        H --> I["rtsp:// → .mp4"]
        I --> J["Return 200"]
    end
    
    subgraph stop["Stop Recording"]
        K["POST /api/recording/stop"] --> L["Stop FFmpeg"]
        L --> M["Decrement RefCount"]
        M --> N["Auto-stop input if RefCount==0"]
    end
```

---

## HLS Viewer Flow

```mermaid
flowchart TB
    subgraph session["HLS Session Lifecycle"]
        A["POST /api/relay/hls/start-viewer"] --> B{"Session exists?"}
        B -->|No| C["Create PipelineHLSSession"]
        B -->|Yes| D["Use existing session"]
        C --> E["Generate viewer_id"]
        D --> E
        E --> F["Add viewer to session"]
        F --> G["Return {viewer_id, playlist_url}"]
    end
    
    subgraph watch["Watch HLS Stream"]
        H["GET /api/relay/watch-input/hls/{name}/index.m3u8"] --> I{"Session ready?"}
        I -->|No| J["Return 503"]
        I -->|Yes| K["Serve .m3u8"]
        K --> L["Serve .ts segments"]
    end
    
    subgraph cleanup["Viewer Cleanup"]
        M["POST /api/relay/hls/stop-viewer"] --> N["Remove viewer_id"]
        N --> O{"Any viewers left?"}
        O -->|No| P["Delete HLS session"]
        O -->|Yes| Q["Keep session"]
    end
```

---

## SSE Real-Time Updates

```mermaid
flowchart LR
    subgraph server["Server"]
        A["Pipeline.SSE"] --> B["SSEBroker"]
        B --> C["fsnotify watcher"]
        C --> D["Broadcast JSON list"]
    end
    
    subgraph clients["Clients"]
        E["Browser<br/>/api/recording/sse"]
        F["curl<br/>/api/recording/sse"]
    end
    
    D --> E
    D --> F
    
    subgraph events["File System Events"]
        G["fsnotify.Write"]
        H["fsnotify.Create"]
        I["fsnotify.Remove"]
    end
    
    G --> C
    H --> C
    I --> C
```

The SSE endpoint now watches the recordings directory using `fsnotify` and sends the full list of recordings as JSON whenever files are created, modified, or deleted.

---

## FFmpeg Process Architecture

```mermaid
flowchart TB
    subgraph factory["FFmpegFactory Interface"]
        A["NewInputProcess()"]
        B["NewOutputProcess()"]
        C["NewHLSProcess()"]
        D["NewRecordProcess()"]
    end
    
    subgraph processes["FFmpegProcess Instances"]
        E["Input Relay<br/>ffmpeg -re -i <src> -f rtsp <rtsp://>"]
        F["Output Relay<br/>ffmpeg <opts> -i <rtsp://> -f flv <dest>"]
        G["HLS Process<br/>ffmpeg <opts> -i <rtsp://> -f hls <dir>"]
        H["Recording Process<br/>ffmpeg -i <rtsp://> -c copy <file>.mp4"]
    end
    
    A --> E
    B --> F
    C --> G
    D --> H
```

---

## RTSP Server Integration

```mermaid
flowchart TB
    subgraph rtsp["RTSPServerManager"]
        A["gortsplib.Server"]
        B["streams map"]
        C["streamReady channels"]
    end
    
    subgraph registration["Stream Registration"]
        D["AddStream(path)"] --> E["Create RTSP stream"]
        E --> F["Signal streamReady"]
        F --> G["Add to streams map"]
    end
    
    subgraph removal["Stream Removal"]
        H["RemoveStream(path)"] --> I["Close stream"]
        I --> J["Remove from map"]
    end
    
    D -.-> A
    H -.-> A
    A -.-> B
    B -.-> G
    B -.-> J
```

---

## Shutdown Sequence

```mermaid
sequenceDiagram
    participant Main
    participant Context as "app.Context"
    participant Pipeline
    participant FFmpeg as "FFmpeg Processes"
    participant SSE as "SSEBroker"
    participant RTSP as "RTSPServer"
    
    Main->>Context: Shutdown()
    Context->>Pipeline: Shutdown()
    Note over Pipeline: Lock mutex<br/>Stop all outputs<br/>Stop all HLS sessions
    Pipeline->>SSE: Shutdown()
    Pipeline->>FFmpeg: Stop(timeout)
    Note over FFmpeg: SIGTERM → SIGKILL
    Context->>RTSP: Stop()
    RTSP-->>Context: Done
    Context-->>Main: Complete
```

---

## Design Principles

### 1. Single Responsibility
The Pipeline manages all streams—inputs, outputs, recordings, HLS sessions—through a unified interface.

### 2. Dependency Injection
All dependencies (FFmpegFactory, RTSPServer) are injected via setters, enabling testing with mocks.

### 3. No Callbacks
Consumer lifecycle is managed directly via goroutines and mutexes—no interface callbacks.

### 4. No Global State
SSEBroker is stored in Pipeline, not as a global variable.

### 5. Fail-Safe Defaults
Reference counting ensures inputs are only stopped when all consumers are done.

---

## File Structure

### internal/stream/ - Streaming Backend
```
internal/stream/
├── pipeline.go         # Core Pipeline struct and methods
├── stream.go          # PipelineStream, Relay, PipelineHLSSession, PipelineRecording types
├── ffmpeg.go          # FFmpegFactory interface + default implementation
├── ffmpeg_process.go  # FFmpegProcess interface + implementation
├── rtsp_server.go     # RTSPServerManager (concrete, not interface)
├── sse.go             # SSEBroker (concrete, not interface)
├── relay_api.go       # Relay HTTP handlers
├── recording_api.go   # Recording HTTP handlers
├── hls_api.go        # HLS HTTP handlers
├── preset_helper.go   # ApplyPresetAndOptions()
└── presets.go        # Platform presets (YouTube, Facebook, etc.)
```

### internal/state/ - State Management
```
internal/state/
└── state.go          # Thread-safe in-memory store for persistence
```

### internal/worker/ - FFmpeg Process Management
```
internal/worker/
├── ffmpeg.go       # RunAndMonitorFFmpeg (core wrapper)
├── puller.go       # Puller - pulls from remote, pushes to local RTMP
├── restreamer.go   # Restreamer - pushes local to remote RTMP
├── recorder.go     # Recorder - records to MP4
└── hls.go         # HLSManager - generates HLS
```

### internal/ingest/ - Stream Ingestion
```
internal/ingest/
└── router.go      # Smart Ingest Router - manages pullers, acceptors, tokens
```

### internal/api/ - HTTP Control Plane
```
internal/api/
└── server.go      # HTTP API server with all REST endpoints
```

---

## Worker Architecture

Workers manage FFmpeg child processes with telemetry:

```
┌─────────────────────────────────────────────────────────────┐
│                      worker.Puller                            │
│  FFmpeg: -re -i <remote_url> -c copy -f flv <local_rtmp>   │
│  Pulls from RTSP/SRT/HLS → Pushes to local RTMP hub         │
└─────────────────────────────────────────────────────────────┘
                              ↓
                    rtmp://127.0.0.1:1935/{streamPath}
                              ↓
┌──────────────┬──────────────┬──────────────┐
│ worker.       │ worker.      │ worker.       │
│ Restreamer   │ Recorder     │ HLSManager   │
│              │              │              │
│ -f flv       │ -c copy      │ -f hls       │
│ <remote_url> │ <file>.mp4   │ <dir>/       │
└──────────────┴──────────────┴──────────────┘
```

---

## Related Documentation

- [API Reference](api-reference.md) - Method signatures and HTTP endpoints
- [Configuration](configuration.md) - JSON config schema
