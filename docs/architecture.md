# Go-MLS Architecture

**Generated**: April 2026  
**Status**: Current Architecture

---

## Executive Summary

Go-MLS is a streaming media gateway that:
- Accepts input streams via configurable hub (RTMP or RTSP)
- Distributes to multiple outputs, recordings, and HLS viewers
- Uses worker-based architecture with FFmpeg processes

---

## Component Overview

```mermaid
flowchart TB
    subgraph main["main.go"]
        A["Entry Point"]
    end
    
    subgraph app["app.Context"]
        B["Hub<br/>(RTMP or RTSP)"]
        C["Ingest Router"]
        D["HLSManager"]
        E["State Store"]
    end
    
    subgraph hub["Hub Interface"]
        F["RTMPHub"]
        G["RTSPHub"]
    end
    
    subgraph workers["Worker Package"]
        H["Puller"]
        I["Restreamer"]
        J["Recorder"]
        K["HLSGenerator"]
    end
    
    A --> B
    B --> F
    B --> G
    C --> H
    H --> B
    B --> I
    B --> J
    B --> K
```

---

## Hub Architecture

The hub is configurable via `relay.hub_type`:

| Hub Type | Protocol | Use Case |
|----------|----------|----------|
| `rtmp` | RTMP | OBS, streaming software |
| `rtsp` | RTSP | IP cameras, NVRs |

```mermaid
flowchart LR
    subgraph config["Configuration"]
        A["hub_type: rtmp"] 
        B["hub_type: rtsp"]
    end
    
    subgraph hubs["Hub Implementations"]
        C["RTMPHub<br/>gortmplib-based"]
        D["RTSPHub<br/>gortsplib-based"]
    end
    
    A --> C
    B --> D
```

---

## Worker Architecture

Workers manage FFmpeg child processes:

```mermaid
flowchart TB
    subgraph ingest["Ingest"]
        A["RTMP/RTSP Publisher"] --> B["Hub"]
        B --> C["Ingest Router"]
    end
    
    subgraph workers["Workers"]
        C --> D["Puller"]
        D --> E["RTMP Push to Hub"]
        
        subgraph consumers["Consumers"]
            F["Restreamer"]
            G["Recorder"]
            H["HLSManager"]
        end
    end
    
    E --> F
    E --> G
    E --> H
```

### Worker Types

| Worker | Purpose | Output |
|--------|---------|--------|
| `Puller` | Pull from remote → push to local RTMP hub | `rtmp://localhost:1935/{stream}` |
| `Restreamer` | Take from hub → push to remote RTMP | RTMP destinations |
| `Recorder` | Take from hub → record to MP4 | `.mp4` files |
| `HLSManager` | Take from hub → generate HLS | `.m3u8` + `.ts` |

---

## State Management

```mermaid
flowchart TB
    subgraph state["State Store"]
        A["Inputs<br/>map[streamPath]*Input"]
        B["Outputs<br/>map[streamPath]map[outputID]*Output"]
        C["Recordings<br/>map[streamPath]*Recording"]
        D["HLSSessions<br/>map[streamPath]*HLSSession"]
    end
```

---

## API Endpoints

| Endpoint | Methods | Description |
|----------|---------|-------------|
| `/inputs` | GET, POST, DELETE | Manage input streams |
| `/outputs` | GET, POST, DELETE | Manage output relays |
| `/record` | POST, DELETE | Start/stop recording |
| `/hls/start` | POST | Start HLS viewer |
| `/hls/stop` | POST | Stop HLS viewer |
| `/stats` | GET | Get system statistics |
| `/system/export` | GET | Export configuration |
| `/system/import` | POST | Import configuration |

---

## Shutdown Sequence

```mermaid
sequenceDiagram
    participant Main
    participant Context as "app.Context"
    participant HLS as "HLSManager"
    participant Hub
    participant Workers
    
    Main->>Context: Shutdown()
    Context->>HLS: Shutdown()
    HLS-->>Context: Done
    Context->>Hub: Stop()
    Hub-->>Context: Done
    Context-->>Main: Complete
```

---

## File Structure

### internal/hub/ - Media Ingestion Hub
```
internal/hub/
├── hub.go    # Hub interface + RTMPHub
└── rtsp.go   # RTSPHub implementation
```

### internal/worker/ - FFmpeg Process Management
```
internal/worker/
├── ffmpeg.go       # RunAndMonitorFFmpeg (core wrapper)
├── ffmpeg_factory.go # Process interface
├── worker.go       # BaseWorker, ProcessWorker, WorkerState
├── puller.go       # Puller - pulls from remote, pushes to hub
├── restreamer.go   # Restreamer - hub to remote RTMP
├── recorder.go     # Recorder - hub to MP4
└── hls.go         # HLSManager - hub to HLS
```

### internal/ingest/ - Stream Ingestion
```
internal/ingest/
└── router.go      # Smart Ingest Router
```

### internal/state/ - State Management
```
internal/state/
└── state.go      # Thread-safe in-memory store
```

### internal/api/ - HTTP Control Plane
```
internal/api/
└── server.go      # HTTP API server
```

### internal/app/ - Application Context
```
internal/app/
└── context.go     # Creates hub, ingest, workers, state
```

---

## Design Principles

### 1. Interface-Based Hub
The `Hub` interface allows swapping between RTMP and RTSP protocols without changing worker code.

### 2. Worker Lifecycle
Workers follow the `Start()` → `Run()` → `Stop()` → `Wait()` pattern with proper goroutine management.

### 3. State-Based Design
Centralized state store for inputs, outputs, recordings, and HLS sessions enables persistence and export/import.

### 4. No Callbacks in Critical Paths
Token validation and publish handlers are synchronous to ensure correctness.

---

## Related Documentation

- [API Reference](api-reference.md) - HTTP endpoints
- [Configuration](configuration.md) - JSON config schema
