# Go-MLS Architecture

**Generated**: April 2026  
**Status**: Current Architecture

---

## Executive Summary

Go-MLS is a streaming media gateway that:
- Accepts input streams via **multi-protocol ingest layer** (RTMP, RTSP, SRT, HLS pull)
- **Bridges all protocols to internal RTMP backbone** for unified distribution
- Distributes to multiple outputs, recordings, and HLS viewers via workers
- Uses worker-based architecture with FFmpeg processes
- Provides HTTP API for management

---

## Component Overview

```mermaid
flowchart TB
    subgraph main["main.go"]
        A["Entry Point<br/>(Signal handling, graceful shutdown)"]
    end
    
    subgraph ingest["Ingest Layer"]
        B["Composite Hub<br/>(Multi-Protocol)"]
        subgraph hubs["Protocol Hubs"]
            B1["RTMP Hub<br/>(Publishers)"]
            B2["RTSP Hub<br/>(Publishers)"]
            B3["SRT Hub<br/>(Publishers)"]
            B4["Internal RTMP<br/>(Backbone)"]
        end
    end
    
    subgraph routing["Ingest Router"]
        C1["Pullers<br/>(Pull from external)"]
        C2["RTSP Adapters<br/>(RTSP→RTMP)"]
        C3["SRT Adapters<br/>(SRT→RTMP)"]
    end
    
    subgraph app["app.Context"]
        D["HLSManager<br/>(HLS generation)"]
        E["State Store<br/>(In-memory state)"]
    end
    
    subgraph api["api.Server"]
        F["HTTP API<br/>(REST endpoints)"]
    end
    
    subgraph workers["Worker Package"]
        G["Restreamer<br/>(Outputs)"]
        H["Recorder<br/>(Recording)"]
    end
    
    A --> F
    A --> B
    A --> E
    F --> C1
    F --> C2
    F --> C3
    C1 --> E
    C2 --> E
    C3 --> E
    B1 --> B4
    B2 --> B4
    B3 --> B4
    C1 -.->|pull from| B4
    C2 -->|bridge to| B4
    C3 -->|bridge to| B4
    B4 --> G
    B4 --> H
    B4 --> D
    E -.-> G
    E -.-> D
```

---

## Multi-Protocol Ingest Layer

Go-MLS accepts streams from multiple protocols simultaneously via a **composite hub architecture**:

```mermaid
flowchart TB
    subgraph sources["External Sources"]
        S1["RTMP Publishers<br/>(OBS, FFmpeg, etc.)"]
        S2["RTSP Cameras<br/>(IP Cameras, NVRs)"]
        S3["SRT Callers<br/>(SRT Encoders)"]
        S4["External RTMP/RTSP/SRT<br/>(Pull mode)"]
    end
    
    subgraph ingest["Composite Hub & Adapters"]
        H1["RTMP Hub<br/>:1935"]
        H2["RTSP Hub<br/>:8554"]
        H3["SRT Hub<br/>:9000"]
        PULLER["Pullers<br/>(FFmpeg)"]
        RTSP_ADP["RTSP Adapters<br/>(FFmpeg bridges)"]
        SRT_ADP["SRT Adapters<br/>(FFmpeg bridges)"]
    end
    
    subgraph backbone["Internal RTMP Backbone"]
        INTERNAL["Internal RTMP Hub<br/>:1935<br/>(localhost only)"]
    end
    
    subgraph distribution["Distribution"]
        RESTREAM["Restreamer<br/>(Outputs)"]
        RECORD["Recorder<br/>(Files)"]
        HLS["HLS Manager<br/>(Playlists)"]
    end
    
    S1 -->|RTMP PUSH| H1
    S2 -->|RTSP PUSH| H2
    S3 -->|SRT PUSH| H3
    S4 -->|PULL via FFmpeg| PULLER
    
    H1 -->|streams| INTERNAL
    PULLER -->|bridge to| INTERNAL
    RTSP_ADP -->|bridge to| INTERNAL
    SRT_ADP -->|bridge to| INTERNAL
    H2 -->|accept-mode| RTSP_ADP
    H3 -->|accept-mode| SRT_ADP
    
    INTERNAL -->|consume| RESTREAM
    INTERNAL -->|consume| RECORD
    INTERNAL -->|consume| HLS
```

### Protocol Support

| Protocol | Role | Port | Transport |
|----------|------|------|-----------|
| **RTMP** | Publisher push (native) | 1935 | TCP, requires gortmplib |
| **RTSP** | Accept-mode push + external pull | 8554 | TCP, requires gortsplib |
| **SRT** | Accept-mode push | 9000 | UDP, requires libsrt |
| **HLS** | Pull-based ingest via FFmpeg Puller | - | HTTP |

### Hub Types Configuration

The primary hub type can be selected via `relay.hub_type` in config:

```mermaid
flowchart LR
    CONFIG["relay.hub_type"]
    
    CONFIG -->|"rtmp"| RTMP_PRIMARY["Primary: RTMP Hub<br/>Secondary: RTSP, SRT, Internal RTMP"]
    CONFIG -->|"rtsp"| RTSP_PRIMARY["Primary: RTSP Hub<br/>Secondary: RTMP, SRT, Internal RTMP"]
    CONFIG -->|"srt"| SRT_PRIMARY["Primary: SRT Hub<br/>Secondary: RTMP, RTSP, Internal RTMP"]
    
    RTMP_PRIMARY --> COMPOSITE["All hubs started<br/>via CompositeHub"]
    RTSP_PRIMARY --> COMPOSITE
    SRT_PRIMARY --> COMPOSITE
    
    COMPOSITE -->|broadcast callbacks| INGEST["Ingest Router"]
```

---

## Ingest Router Architecture

The Ingest Router coordinates different stream ingestion modes:

```mermaid
flowchart TB
    REGISTER["RegisterInput<br/>via API"]
    
    REGISTER -->|pull_protocol| CHECK_PULL{Pull Mode?}
    CHECK_PULL -->|pull from external| PULLER["Puller<br/>(FFmpeg pull)"]
    CHECK_PULL -->|accept from publish| CHECK_ACCEPT{Accept Mode?}
    
    CHECK_ACCEPT -->|accept_protocol: rtsp| RTSP_LISTEN["RTSP Hub<br/>listens on :8554"]
    CHECK_ACCEPT -->|accept_protocol: srt| SRT_LISTEN["SRT Hub<br/>listens on :9000"]
    CHECK_ACCEPT -->|accept_protocol: rtmp| RTMP_LISTEN["RTMP Hub<br/>listens on :1935"]
    
    PULLER -.->|OnPublish callback<br/>triggered| ROUTE["onPublish handler<br/>bridges to RTMP backbone"]
    RTSP_LISTEN -.->|publisher connects<br/>OnPublish callback| RTSP_ADP["RTSPAdapter<br/>FFmpeg bridge<br/>rtsp://127.0.0.1:8554<br/>→ rtmp://127.0.0.1:1935"]
    SRT_LISTEN -.->|publisher connects<br/>OnPublish callback| SRT_ADP["SRTAdapter<br/>FFmpeg bridge<br/>srt://127.0.0.1:9000<br/>→ rtmp://127.0.0.1:1935"]
    RTMP_LISTEN -.->|publisher connects<br/>OnPublish callback| RTMP_DIRECT["Direct RTMP<br/>stream available<br/>on backbone"]
    
    ROUTE --> INTERNAL["Internal RTMP Backbone<br/>:1935 (localhost)"]
    RTSP_ADP --> INTERNAL
    SRT_ADP --> INTERNAL
    RTMP_DIRECT --> INTERNAL
```

### Input Registration Details

```mermaid
flowchart LR
    API["POST /inputs"]
    
    API -->|pull_protocol: rtmp/rtsp/srt/hls| PULLER_START["NewPuller<br/>FFmpeg starts"]
    API -->|pull_protocol: false| ACCEPT_MODE
    
    ACCEPT_MODE -->|accept_protocol: rtmp| REGISTER_RTMP["Register with<br/>RTMP Hub<br/>via OnPublish"]
    ACCEPT_MODE -->|accept_protocol: rtsp| REGISTER_RTSP["Register with<br/>RTSP Hub<br/>TLS credentials"]
    ACCEPT_MODE -->|accept_protocol: srt| REGISTER_SRT["Register with<br/>SRT Hub<br/>SRT params"]
    
    PULLER_START --> READY["Input.Status<br/>= Active"]
    REGISTER_RTMP --> WAITING["Input.Status<br/>= Waiting"]
    REGISTER_RTSP --> WAITING
    REGISTER_SRT --> WAITING
    
    WAITING -->|publisher connects| ACTIVE["Input.Status<br/>= Active"]
```

---

## Stream Flow: From Ingest to Distribution

```mermaid
flowchart TB
    subgraph sources["1. Input Sources"]
        IN_RTMP["RTMP Push<br/>(OBS)"]
        IN_RTSP["RTSP Accept-Mode<br/>(Camera)"]
        IN_SRT["SRT Accept-Mode<br/>(Encoder)"]
        IN_PULL["Pull-mode FFmpeg<br/>(Remote source)"]
    end
    
    subgraph hubs["2. Protocol Hubs"]
        HUB_RTMP["RTMP Hub<br/>:1935"]
        HUB_RTSP["RTSP Hub<br/>:8554"]
        HUB_SRT["SRT Hub<br/>:9000"]
    end
    
    subgraph adapters["3. Protocol Bridges"]
        ADP_RTSP["RTSP Adapter<br/>FFmpeg:tcp"]
        ADP_SRT["SRT Adapter<br/>FFmpeg:udp"]
    end
    
    subgraph backbone["4. Internal RTMP Backbone"]
        INTERNAL["Internal RTMP Hub<br/>:1935 (127.0.0.1)"]
    end
    
    subgraph workers["5. Workers Consume<br/>from Backbone"]
        W1["Restreamer<br/>(ffmpeg -i rtmp://...)"]
        W2["Recorder<br/>(ffmpeg -i rtmp://...)"]
        W3["HLS Manager<br/>(ffmpeg -i rtmp://...)"]
    end
    
    IN_RTMP -.->|rtmp://server:1935| HUB_RTMP
    IN_RTSP -.->|rtsp://server:8554| HUB_RTSP
    IN_SRT -.->|srt://server:9000| HUB_SRT
    IN_PULL -.->|external url| PULLER_NODE["Puller<br/>FFmpeg"]
    
    HUB_RTMP -->|streams available<br/>on backbone| INTERNAL
    PULLER_NODE -->|onPublish to backbone| INTERNAL
    HUB_RTSP -->|accept connect| ADP_RTSP
    HUB_SRT -->|accept connect| ADP_SRT
    ADP_RTSP -->|ffmpeg -i rtsp://127.0.0.1:8554<br/>-f flv tcp:27.0.0.1:1935| INTERNAL
    ADP_SRT -->|ffmpeg -i srt://127.0.0.1:9000<br/>-f flv tcp:127.0.0.1:1935| INTERNAL
    
    INTERNAL -->|rtmp://127.0.0.1:1935/stream-path| W1
    INTERNAL -->|rtmp://127.0.0.1:1935/stream-path| W2
    INTERNAL -->|rtmp://127.0.0.1:1935/stream-path| W3
    
    W1 -->|TCP push| OUT["Output<br/>Destinations"]
    W2 --> REC["Recording Files<br/>disk/"]
    W3 --> PLAY["HLS Playlists<br/>HTTP viewers"]
```

---

## Hub Architecture (Updated)

The hub is configurable via `relay.hub_type` and supports multiple simultaneous protocols:

```mermaid
flowchart LR
    subgraph config["Configuration"]
        A["relay.hub_type:<br/>rtmp|rtsp|srt"] 
        B["relay.rtmp_hub:<br/>host:port"]
        C["relay.rtsp_hub:<br/>host:port"]
        D["relay.srt_hub:<br/>host:port"]
    end
    
    subgraph hubs["Hub Implementations"]
        H1["RTMPHub<br/>(gortmplib)"]
        H2["RTSPHub<br/>(gortsplib)"]
        H3["SRTHub<br/>(libsrt)"]
        H4["Internal RTMP<br/>(gortmplib)"]
    end
    
    subgraph composite["CompositeHub"]
        COMP["Manages all hubs<br/>with shared callbacks<br/>OnPublish / OnUnpublish"]
    end
    
    A --> COMP
    B --> H1
    C --> H2
    D --> H3
    
    H1 --> COMP
    H2 --> COMP
    H3 --> COMP
    H4 --> COMP
```

### Hub Type Primary Selection

| Config | Primary Hub | Secondary Hubs | Use Case |
|--------|-------------|----------------|----------|
| `rtmp` | RTMP Hub | RTSP, SRT, Internal | OBS/streaming software primary push |
| `rtsp` | RTSP Hub | RTMP, SRT, Internal | IP cameras primary push |
| `srt` | SRT Hub | RTMP, RTSP, Internal | SRT encoders primary push |

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

## Worker Architecture

Workers manage FFmpeg child processes with proper lifecycle management:

```mermaid
flowchart TB
    subgraph ingest["Internal RTMP Backbone"]
        BACKBONE["RTMP Backbone<br/>:1935 (127.0.0.1)"]
    end
    
    subgraph workers["Consumers from Backbone"]
        C1["Restreamer<br/>ffmpeg -i rtmp://127.0.0.1:1935<br/>→ remote destination"]
        C2["Recorder<br/>ffmpeg -i rtmp://127.0.0.1:1935<br/>→ MP4 file"]
        C3["HLSManager<br/>ffmpeg -i rtmp://127.0.0.1:1935<br/>→ HLS segments"]
    end
    
    BACKBONE --> C1
    BACKBONE --> C2
    BACKBONE --> C3
```

### Worker Types

| Worker | Purpose | Managed By | Input |
|--------|---------|------------|-------|
| `Puller` | Pull from remote URL (RTMP/RTSP/SRT/HLS) → push to local RTMP hub | Ingest Router | External sources |
| `RTSPAdapter` | Accept RTSP → bridge to internal RTMP backbone | Ingest Router | RTSP Hub accept-mode |
| `SRTAdapter` | Accept SRT → bridge to internal RTMP backbone | Ingest Router | SRT Hub accept-mode |
| `Restreamer` | Take from backbone → push to remote RTMP | API Server | Internal RTMP |
| `Recorder` | Take from backbone → record to MP4 | API Server | Internal RTMP |
| `HLSManager` | Take from backbone → generate HLS segments | API Server | Internal RTMP |

### Ingest Router Worker Management

```mermaid
flowchart TB
    INPUT["RegisterInput<br/>via API"]
    
    INPUT --> DECISION{"Input<br/>Configuration"}
    
    DECISION -->|pull_protocol:<br/>rtmp/rtsp/srt/hls| PULLER_CREATE["Create Puller<br/>ffmpeg -i [remote_url]<br/>-f flv tcp://127.0.0.1:1935"]
    DECISION -->|pull_protocol: false<br/>accept_protocol: rtmp| RTMP_MODE["RTMP Native<br/>Hub accepts<br/>on :1935"]
    DECISION -->|pull_protocol: false<br/>accept_protocol: rtsp| RTSP_ACCEPT["RTSP Accept<br/>Hub listens :8554<br/>→ RTSPAdapter"]
    DECISION -->|pull_protocol: false<br/>accept_protocol: srt| SRT_ACCEPT["SRT Accept<br/>Hub listens :9000<br/>→ SRTAdapter"]
    
    PULLER_CREATE -.->|Start worker| BACKBONE["Internal RTMP<br/>Backbone"]
    RTMP_ACCEPT -.->|Flows to| BACKBONE
    RTSP_ACCEPT -.->|Bridges via<br/>FFmpeg| BACKBONE
    SRT_ACCEPT -.->|Bridges via<br/>FFmpeg| BACKBONE
```

---

## Runtime Lifecycle Sequences

### Import Rebuild Sequence

The import path clears runtime state, re-registers inputs, restores output definitions,
waits for pull inputs to become active, and then starts outputs. The HTTP request returns `202 Accepted` before this rebuild finishes, so follow-up control calls can briefly overlap the async restore window.

```mermaid
sequenceDiagram
    participant Client as Client
    participant API as API Server
    participant Store as State Store
    participant Ingest as Ingest Router
    participant Hub as Hub (RTMP/RTSP)
    participant Worker as Puller/Restreamer Workers

    Client->>API: POST /system/import
    API-->>Client: 202 Accepted
    API->>API: stopAllOutputs(), stop active recording/HLS
    API->>Worker: Wait for old workers to drain
    API->>Ingest: Unregister all inputs
    Ingest->>Hub: EvictStream(stream_path)
    Ingest->>Worker: Stop puller (if any)
    API->>Store: Clear runtime input/output state
    loop For each imported input
        API->>Ingest: RegisterInput(input)
        alt Pull input
            API->>Ingest: EnsureInputActive(stream_path)
            Ingest->>Worker: Start puller
        else Accept input
            Note over Ingest,Hub: Wait for publisher
        end
    end
    API->>Store: Add output definitions
    loop For each imported output
        API->>Store: Reserve output startup slot
        API->>API: waitForInputActive(stream_path)
        API->>Worker: Start output worker
    end
```

### Input Delete Cleanup Sequence

Deleting an input now performs explicit runtime teardown for outputs, recordings,
HLS generation, pullers, and hub publishers before removing state.

```mermaid
sequenceDiagram
    participant Client as Client
    participant API as API Server
    participant Worker as Output/Record/HLS Workers
    participant Ingest as Ingest Router
    participant Hub as Hub (RTMP/RTSP)
    participant Store as State Store

    Client->>API: DELETE /inputs?stream=...
    API->>Worker: Stop/remove outputs for stream
    API->>Worker: Stop recording for stream (if active)
    API->>Worker: Stop HLS stream (if active)
    API->>Ingest: UnregisterInput(stream_path)
    Ingest->>Worker: Stop puller (if owned)
    Ingest->>Hub: EvictStream(stream_path)
    API->>Store: Remove input + child runtime entries
    API-->>Client: 200 OK
```

---

## State Management

### State Store Structure

```mermaid
flowchart TB
    subgraph state["State Store"]
        A["Inputs<br/>map[streamPath]*Input"]
        B["Outputs<br/>map[streamPath/outputID]*Output"]
        C["Recordings<br/>map[streamPath]*Recording"]
        D["HLSSessions<br/>map[streamPath]*HLSSession"]
        E["Telemetry<br/>map[pid]*Telemetry"]
    end
```

For practical read/write behavior, hot paths, and scan tradeoffs, see `docs/state-and-stats.md`.

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

Runtime list APIs return value snapshots (not shared pointers), so read-heavy handlers like `/stats` can iterate safely while writers update status/PID fields under store locks.

The `/stats` endpoint itself is backed by a background-refreshed cache: a periodic goroutine samples self usage, reads store snapshots and telemetry, pre-encodes the JSON response, and atomically swaps the latest payload for request handlers to serve.

### 4. No Callbacks in Critical Paths
Token validation and publish handlers are synchronous to ensure correctness.

### 5. Process Group Isolation
FFmpeg processes run in their own process group to prevent SIGTERM propagation from parent.

### 6. Graceful Shutdown with Timeout
Workers receive SIGTERM first (5s timeout), then SIGKILL if needed.

Hub lifecycle (`Start()`/`Stop()`) is serialized internally to avoid concurrent lifecycle races and to support safe restart after stop.

---

## Related Documentation

- [Worker FSM Design](worker-fsm.md) - Detailed state machine diagrams
- [API Reference](api-reference.md) - HTTP endpoints
- [Configuration](configuration.md) - JSON config schema
- [State And Stats Guide](state-and-stats.md) - Data model keys, access patterns, frequencies
