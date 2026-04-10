# Ingest Layer Architecture & Protocol Bridging

**Status**: Current (April 2026)

This document describes how Go-MLS ingests streams from multiple protocols (RTMP, RTSP, SRT, HLS) and bridges them to a unified internal RTMP backbone.

---

## Overview

The ingest layer consists of:

1. **Composite Hub** - Manages multiple protocol listeners simultaneously
2. **Protocol Hubs** - RTMP, RTSP, SRT native listeners
3. **Ingest Router** - Coordinates input registration and worker lifecycle
4. **Protocol Adapters** - FFmpeg bridges for RTSP and SRT accept-mode inputs
5. **Pullers** - FFmpeg pullers for remote source ingest
6. **Internal RTMP Backbone** - Unified convergence point for all streams

---

## Composite Hub

The `CompositeHub` manages multiple protocol listeners as a single hub interface:

```
CompositeHub
├── Primary Hub (RTMP/RTSP/SRT based on config)
└── Secondary Hubs (RTMP, RTSP, SRT, Internal RTMP)
```

**Key behaviors:**
- Starts all configured hubs in sequence
- Broadcasts `OnPublish` and `OnUnpublish` callbacks to all hubs
- Stops all hubs on shutdown in reverse order
- Reports primary hub address via `Addr()`

**Example flow:**

```go
// In app/context.go
internalRTMPHub := hub.NewRTMPHub(...)  // Internal backbone
rtspHub := hub.NewRTSPHub(...)          // RTSP listener
srtHub := hub.NewSRTHub(...)            // SRT listener
primaryHub := rtspHub                     // config says hub_type: "rtsp"

ctx.Hub = hub.NewCompositeHub(primaryHub, internalRTMPHub, rtspHub, srtHub)
```

### Callback Propagation

When a publisher connects to any hub, the `OnPublish` callback is triggered for all hubs:

```
Publisher connects to RTSP Hub
    ↓
RTSP Hub calls OnPublish
    ↓
CompositeHub propagates to all hubs
    ↓
RTMPHub, SRTHub, Internal RTMPHub receive callback
```

The Ingest Router listens to these callbacks to trigger protocol adapters.

---

## Ingest Router

The Ingest Router (`internal/ingest/router.go`) manages input lifecycle:

### Input Registration Modes

1. **Pull Mode**: External source pulled via FFmpeg Puller
2. **Accept RTMP**: RTMP publisher sends direct to hub
3. **Accept RTSP**: RTSP publisher triggers RTSPAdapter
4. **Accept SRT**: SRT publisher triggers SRTAdapter

### RegisterInput Flow

```
API: POST /inputs
  ↓
Router.RegisterInput()
  ├─ If pull_protocol: Create and start Puller
  │  └─ Puller FFmpeg: ffmpeg -i [external_url] -f flv tcp://127.0.0.1:1935
  │
  └─ If accept_protocol: Register with appropriate hub via state
     ├─ accept_protocol: "rtmp" → RTMP Hub ready (no adapter needed)
     ├─ accept_protocol: "rtsp" → RTSP Hub ready (adapter triggered on connect)
     └─ accept_protocol: "srt" → SRT Hub ready (adapter triggered on connect)
```

### Ingest Router Data Structures

```go
type Router struct {
    store        *state.Store
    log          *logger.Logger
    rtmpPort     int
    rtspPort     int
    srtHost      string
    srtPort      int
    
    // Pull-mode workers
    pullers      map[string]*worker.Puller
    
    // Accept-mode adapters (triggered by OnPublish)
    srtAdapters  map[string]*worker.SRTAdapter
    rtspAdapters map[string]*worker.RTSPAdapter
}
```

---

## Protocol Adapters

Protocol adapters are FFmpeg-based bridges that run when an accept-mode input is published to.

### RTSP Adapter

**Trigger**: When a stream connects to `rtsp://server:8554/[stream-path]`

**Process:**
1. RTSP Hub receives publish, calls `OnPublish(stream_path, token, remoteAddr)`
2. Ingest Router checks state for input definition with `accept_protocol: "rtsp"`
3. If found, spawns `RTSPAdapter` for that stream
4. Adapter runs FFmpeg:
   ```bash
   ffmpeg -rtsp_transport tcp \
          -i rtsp://127.0.0.1:8554/[stream-path] \
          -c copy \
          -f flv tcp://127.0.0.1:1935/[stream-path]
   ```
5. Stream is now available on internal RTMP backbone for workers

**Key code:**
```go
// RTSPAdapter wraps a ProcessWorker
type RTSPAdapter struct {
    *ProcessWorker
    store      *state.Store
    stream     *state.Input
    rtspPort   int
    rtmpPort   int
    localInput string  // rtsp://127.0.0.1:8554/...
}
```

**Lifecycle:**
- Starts when publisher connects (OnPublish)
- Stops when publisher disconnects or input is deleted
- Monitored by ProcessWorker for FFmpeg restarts on failure

### SRT Adapter

**Trigger**: When a stream connects to `srt://server:9000?mode=listener&streamid=publish:[stream-path]`

**Process:**
1. SRT Hub receives publish, calls `OnPublish(stream_path, token, remoteAddr)`
2. Ingest Router checks state for input definition with `accept_protocol: "srt"`
3. If found, spawns `SRTAdapter` for that stream
4. Adapter runs FFmpeg:
   ```bash
   ffmpeg -i srt://127.0.0.1:9000/[stream-path] \
          -c copy \
          -f flv tcp://127.0.0.1:1935/[stream-path]
   ```
5. Stream is now available on internal RTMP backbone

**SRT Publisher Example:**
```bash
# OBS output with SRT destination
ffmpeg -i input.mp4 \
       -c copy \
       -f mpegts \
       'srt://server:9000?mode=caller&streamid=publish:my-stream'

# Or in OBS: Server: srt://server, Port: 9000, Passphrase: (optional)
```

---

## Pullers: Pull-Mode Input Ingestion

Pullers handle external source ingest via FFmpeg.

**When started:**
- Input registered with `pull_protocol: "rtmp"` | `"rtsp"` | `"srt"` | `"hls"`
- Puller FFmpeg is spawned immediately

**How it works:**
```bash
ffmpeg -i [external_url] -f flv tcp://127.0.0.1:1935/[stream-path]
```

**Supported sources:**
- `rtmp://external.com:1935/stream`
- `rtsp://camera.local:554/h264`
- `srt://encoder.com:9000`
- `https://example.com/stream.m3u8` (HLS)

**Lifecycle:**
- Starts immediately when input is registered
- Runs until input is deleted or puller fails
- Monitored for FFmpeg process health, restarts on exit

**Key code:**
```go
type Puller struct {
    *ProcessWorker
    source      string  // external URL
    destination string  // rtmp://127.0.0.1:1935/stream-path
    stream      *state.Input
}
```

---

## Internal RTMP Backbone

All protocols converge to an internal RTMP hub running on `127.0.0.1:1935`.

**Characteristics:**
- **Isolated**: Localhost only, not exposed externally
- **Purpose**: Unified distribution point for all ingested streams
- **Consumers**:
  - Restreamer: `ffmpeg -i rtmp://127.0.0.1:1935/[stream] -f flv tcp://destination`
  - Recorder: `ffmpeg -i rtmp://127.0.0.1:1935/[stream] -c copy output.mp4`
  - HLS Manager: `ffmpeg -i rtmp://127.0.0.1:1935/[stream] -hls_playlist_type event out.m3u8`
- **Startup**: Automatically created and started by CompositeHub

**RTMP stream path consistency:**
All streams on the backbone use the same path as the input:
- RTMP publisher: `rtmp://server:1935/live` → backbone has `rtmp://127.0.0.1:1935/live`
- RTSP adapter: `rtsp://server:8554/camera1` → backbone has `rtmp://127.0.0.1:1935/camera1`
- SRT adapter: `srt://server:9000` with streamid=`publish:encoder1` → backbone has `rtmp://127.0.0.1:1935/encoder1`
- Puller: Pull `rtsp://camera/h264` to `rtmp://127.0.0.1:1935/stream-path`

---

## Full Stream Flow Diagram

```
External Sources
├─ RTMP Encoder (OBS)
│  └─ rtmp://server:1935/live
│     ↓
│   RTMP Hub ← ← ← accepts directly
│
├─ RTSP Camera
│  └─ rtsp://camera:554/h264
│     ├─ Accept-mode: rtsp://server:8554/camera
│     │  ↓
│     │  RTSP Hub ← ← ← accepts publisher
│     │  ↓
│     │  RTSPAdapter (FFmpeg)
│     │  ffmpeg -i rtsp://127.0.0.1:8554/camera -f flv tcp://127.0.0.1:1935
│     │
│     └─ Pull-mode: pull_protocol: "rtsp"
│        ↓
│        Puller (FFmpeg)
│        ffmpeg -i rtsp://camera:554/h264 -f flv tcp://127.0.0.1:1935
│
├─ SRT Encoder
│  └─ srt://server:9000?streamid=publish:encoder1
│     ↓
│     SRT Hub ← ← ← accepts caller
│     ↓
│     SRTAdapter (FFmpeg)
│     ffmpeg -i srt://127.0.0.1:9000 -f flv tcp://127.0.0.1:1935/encoder1
│
└─ External RTMP/HLS Source
   └─ Pull-mode: pull_protocol: "rtmp" | "hls"
      ↓
      Puller (FFmpeg)
      ffmpeg -i [external_url] -f flv tcp://127.0.0.1:1935

        ↓ ↓ ↓ ↓ ↓
        
Internal RTMP Backbone (127.0.0.1:1935)
├─ Stream: "live"
├─ Stream: "camera"
├─ Stream: "encoder1"
└─ Stream: "remote-source"

        ↓ ↓ ↓ ↓ ↓

Workers consume backbone
├─ Restreamer → remote outputs (TCP push)
├─ Recorder → files (MP4)
└─ HLS Manager → segments (HTTP)
```

---

## Configuration Example: Mixed Ingest

```json
{
  "relay": {
    "hub_type": "rtmp",
    "rtmp_hub": { "host": "0.0.0.0", "port": 1935 },
    "rtsp_hub": { "host": "0.0.0.0", "port": 8554 },
    "srt_hub": { "host": "0.0.0.0", "port": 9000 }
  }
}
```

### Register Inputs via API

```bash
# OBS pushing RTMP
POST /inputs
{
  "stream_path": "obs-feed",
  "accept_protocol": "rtmp"
}

# IP Camera pushing RTSP
POST /inputs
{
  "stream_path": "camera-1",
  "accept_protocol": "rtsp"
}

# SRT Encoder
POST /inputs
{
  "stream_path": "encoder-1",
  "accept_protocol": "srt"
}

# Pull from remote RTSP source
POST /inputs
{
  "stream_path": "remote-camera",
  "pull_protocol": "rtsp",
  "url": "rtsp://192.168.1.100:554/stream"
}

# Pull from external RTMP source
POST /inputs
{
  "stream_path": "external",
  "pull_protocol": "rtmp",
  "url": "rtmp://external.cdn:1935/live/stream"
}
```

After registration:
1. All streams are available on internal RTMP: `rtmp://127.0.0.1:1935/[stream-path]`
2. Workers consume from backbone
3. One Config to rule them all: mix RTMP, RTSP, SRT, HLS in one Go-MLS instance

---

## Timeout & Resilience

### Input Timeouts

```
relay.input_timeout: How long to wait for pulled input to establish
                     (default: 30s)
```

### Output Timeouts

```
relay.output_timeout: How long FFmpeg output push is allowed to idle
                      (default: 60s)
```

### Adapter Lifecycle

Protocol adapters are monitored via `ProcessWorker`:
- If FFmpeg exits, it's restarted automatically
- Monitored logs for errors, stored in input state
- Can be forcibly stopped via `DELETE /inputs?stream=...`

---

## State Management

Input state tracks:
- `stream_path`: Unique identifier
- `status`: Active | Waiting | Error
- `pull_protocol`: rtmp | rtsp | srt | hls (or nil for accept-mode)
- `accept_protocol`: rtmp | rtsp | srt
- `url`: Remote URL (for pull-mode)
- `pid`: FFmpeg process ID (puller or adapter)
- `error`: Last error message

Example state entry:
```json
{
  "stream_path": "camera-1",
  "status": "Active",
  "accept_protocol": "rtsp",
  "pid": 12345
}
```

---

## Testing the Ingest Layer

### Test RTMP Direct Push
```bash
ffmpeg -f lavfi -i testsrc=s=320x240:d=10 \
       -c:v libx264 -f flv rtmp://localhost:1935/test
```

### Test RTSP Accept-Mode

1. Register input: `POST /inputs` with `accept_protocol: "rtsp"`
2. Push stream:
```bash
ffmpeg -f lavfi -i testsrc=s=320x240:d=10 \
       -c:v libx264 -f rtsp rtsp://localhost:8554/test
```

### Test SRT Accept-Mode

1. Register input: `POST /inputs` with `accept_protocol: "srt"`
2. Push stream:
```bash
ffmpeg -f lavfi -i testsrc=s=320x240:d=10 \
       -c:v libx264 -f mpegts \
       'srt://localhost:9000?mode=caller&streamid=publish:test'
```

### Test Pull Mode (RTSP)

1. Create a test source:
```bash
ffmpeg -f lavfi -i testsrc=s=320x240:d=3600 \
       -c:v libx264 -f rtsp rtsp://localhost:8554/source
```

2. Register pull input: `POST /inputs` with `pull_protocol: "rtsp"` and `url: "rtsp://localhost:8554/source"`

3. Stream should appear on backbone and be available for workers

---

## Debugging

### Check Hub Status
```bash
curl http://localhost:8080/inputs
curl http://localhost:8080/stats
```

### Monitor Logs
```
grep "OnPublish\|RTSPAdapter\|SRTAdapter\|Puller" logs
```

### Check RTMP Backbone
All registered inputs should be available on `rtmp://127.0.0.1:1935/[stream-path]`

### Verify Adapter Running
```bash
ps aux | grep ffmpeg | grep -E "rtsp_adapter|srt_adapter|puller"
```

---

## Summary

The ingest layer provides a unified, multi-protocol entry point:

1. **Composite Hub** starts all protocol listeners simultaneously
2. **Ingest Router** coordinates input registration and worker lifecycle
3. **Protocol Adapters** (RTSP, SRT) bridge external formats to internal RTMP
4. **Pullers** ingest remote sources via FFmpeg
5. **Internal RTMP Backbone** converges all streams for unified distribution

This design allows Go-MLS to accept streams from any source (RTMP, RTSP, SRT, HLS) while maintaining a clean, unified internal architecture for distribution.
