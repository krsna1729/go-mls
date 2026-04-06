# Go-MLS Architecture

**Generated**: April 2026  
**Status**: Current Architecture

---

## Executive Summary

Go-MLS is a streaming media gateway that:
- Accepts input streams via configurable hub (RTMP or RTSP)
- Distributes to multiple outputs, recordings, and HLS viewers
- Uses worker-based architecture with FFmpeg processes
- Provides HTTP API for management

---

## Component Overview

```mermaid
flowchart TB
    subgraph main["main.go"]
        A["Entry Point<br/>(Signal handling, graceful shutdown)"]
    end
    
    subgraph app["app.Context"]
        B["Hub<br/>(RTMP or RTSP)"]
        C["Ingest Router<br/>(Pull/Push management)"]
        D["HLSManager<br/>(HLS generation)"]
        E["State Store<br/>(In-memory state)"]
    end
    
    subgraph api["api.Server"]
        F["HTTP API<br/>(REST endpoints)"]
    end
    
    subgraph hub["Hub Interface"]
        G["RTMPHub"]
        H["RTSPHub"]
    end
    
    subgraph workers["Worker Package"]
        I["Puller"]
        J["Restreamer"]
        K["Recorder"]
    end
    
    A --> F
    A --> B
    A --> app
    F --> C
    F --> E
    C --> I
    C --> E
    B --> F
    B --> G
    B --> H
    I --> B
    B --> J
    B --> K
    D --> E
```

---

## Shutdown Sequence (Critical Path)

Graceful shutdown follows a strict order to ensure no goroutine leaks:

```mermaid
sequenceDiagram
    participant OS as "OS Signal<br/>(SIGTERM/SIGINT)"
    participant Main as "main()"
    participant HTTPServer as "HTTP Server"
    participant Server as "api.Server<br/>(Workers)"
    participant App as "app.Context<br/>(Hub, Ingest)"
    participant HLS as "HLSManager"
    participant Ingest as "Ingest Router"
    participant Hub as "Hub"
    
    OS->>Main: Signal received
    Main->>Main: Create 30s timeout context
    
    Note over Main,HTTPServer: Phase 1: Stop accepting new requests
    Main->>HTTPServer: Shutdown(timeout)
    HTTPServer-->>Main: Done (no new requests)
    
    Note over Main,Server: Phase 2: Stop all workers
    Main->>Server: Shutdown()
    Server->>J: Stop() for each Restreamer
    Server->>K: Stop() for each Recorder
    Server-->>Main: Done (wait for completion)
    
    Note over Main,App: Phase 3: Stop application
    Main->>App: Shutdown()
    App->>HLS: Shutdown()
    App->>Ingest: Shutdown()
    Ingest->>I: Stop() for each Puller
    Ingest-->>App: Done (wait for completion)
    App->>Hub: Stop()
    Hub-->>App: Done
    
    Note over Main: Phase 4: Resource check
    Main->>Main: Report goroutine usage
```

### Shutdown Guarantees

1. **HTTP Server** stops accepting new requests first
2. **Workers** (Restreamers, Recorders) are stopped and waited
3. **Pullers** are stopped and waited
4. **Hub** connections are closed
5. **Final check** reports any remaining goroutines

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

Workers manage FFmpeg child processes with proper lifecycle management:

```mermaid
flowchart TB
    subgraph ingest["Input Sources"]
        A1["RTMP Publisher<br/>(OBS)"]
        A2["HTTP/RTSP Pull<br/>(URL)"]
    end
    
    subgraph hub["Hub"]
        B["Hub<br/>(RTMP/RTSP)"]
    end
    
    subgraph workers["Workers"]
        C1["Puller<br/>Pulls from remote<br/>Pushes to hub"]
        C2["Restreamer<br/>Hub to remote RTMP"]
        C3["Recorder<br/>Hub to MP4"]
        C4["HLSGenerator<br/>Hub to HLS"]
    end
    
    A1 -->|Push| B
    A2 -->|StartPuller| C1
    C1 -->|Push| B
    B --> C2
    B --> C3
    B --> C4
```

### Worker Types

| Worker | Purpose | Managed By |
|--------|---------|------------|
| `Puller` | Pull from remote URL → push to local RTMP hub | Ingest Router |
| `Restreamer` | Take from hub → push to remote RTMP | API Server |
| `Recorder` | Take from hub → record to MP4 | API Server |
| `HLSManager` | Take from hub → generate HLS segments | API Server |

---

## State Management

### State Store Structure

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
| `/outputs/start` | POST | Restart an existing output in place |
| `/outputs/stop` | POST | Stop an existing output without deleting it |
| `/record` | POST, DELETE | Start/stop recording |
| `/recordings` | GET, DELETE | List recordings from disk and delete completed files |
| `/recordings/sse` | GET | Push recording refresh notifications to the web UI |
| `/hls/start` | POST | Start HLS viewer |
| `/hls/stop` | POST | Stop HLS viewer |
| `/hls/heartbeat` | POST | Refresh HLS viewer heartbeat |
| `/stats` | GET | Get system statistics |
| `/system/export` | GET | Export configuration |
| `/system/import` | POST | Import configuration |

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
├── hls.go                # HLSManager, HLSSession lifecycle
├── process_errors.go     # Worker-level process error mapping
├── puller.go             # Puller - pulls from remote, pushes to hub
├── recorder.go           # Recorder - hub to MP4
├── restreamer.go         # Restreamer - hub to remote RTMP
└── worker.go             # BaseWorker, ProcessWorker, Worker state machine
```

### internal/ingest/ - Stream Ingestion
```
internal/ingest/
└── router.go      # Smart Ingest Router (pullers + acceptors)
```

### internal/state/ - State Management
```
internal/state/
├── presets.go     # Built-in output presets and ffmpeg args
└── state.go       # Thread-safe in-memory store and state types
```

### internal/api/ - HTTP Control Plane
```
internal/api/
└── server.go     # HTTP API server with all endpoints
```

### internal/app/ - Application Context
```
internal/app/
└── context.go    # Creates hub, ingest, workers, state
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

### 5. Process Group Isolation
FFmpeg processes run in their own process group to prevent SIGTERM propagation from parent.

### 6. Graceful Shutdown with Timeout
Workers receive SIGTERM first (5s timeout), then SIGKILL if needed.

---

## Related Documentation

- [Worker FSM Design](worker-fsm.md) - Detailed state machine diagrams
- [API Reference](api-reference.md) - HTTP endpoints
- [Configuration](configuration.md) - JSON config schema
