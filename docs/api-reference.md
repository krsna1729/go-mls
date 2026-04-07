# Go-MLS API Reference

**Generated**: April 2026  
**Status**: Current refactored HTTP control plane

---

## Table of Contents

1. [Overview](#overview)
2. [Core Runtime Types](#core-runtime-types)
3. [HTTP API Endpoints](#http-api-endpoints)
4. [Export and Import Format](#export-and-import-format)
5. [Presets](#presets)
6. [State and Stats Behavior](#state-and-stats-behavior)

---

## Overview

Go-MLS exposes a REST API for managing:

- Inputs registered as either pull or accept-mode push sources
- Outputs that can be created, stopped, restarted, or deleted independently
- Recordings stored on disk and listed from the filesystem
- HLS viewer sessions with explicit `viewer_id` heartbeat tracking
- System export/import using the `relay_config.json` schema

The current control plane lives in `internal/api/server.go` and is mounted with these top-level routes:

- `/inputs`
- `/outputs`
- `/outputs/start`
- `/outputs/stop`
- `/presets`
- `/record`
- `/recordings`
- `/recordings/sse`
- `/stats`
- `/system/export`
- `/system/import`
- `/hls/start`
- `/hls/stop`
- `/hls/heartbeat`
- `/hls/...` for HLS playlist and segments
- `/recordings/...` for recorded file download/serving

---

## Core Runtime Types

The API is backed by the in-memory store in `internal/state`.

### Input

```go
type Input struct {
    StreamPath  string
    Mode        InputMode
    Status      InputStatus
    RemoteURL   string
    RemoteAddr  string
    IngestToken string
    LastError   string
    PID         int
}
```

Notes:

- `Mode` is `Pull` when `remote_url` is configured.
- `Mode` is `Accept` for push ingest where publishers connect to the hub.
- `RemoteAddr` is updated at runtime for push ingest when the publisher lands on the hub.

### Output

```go
type Output struct {
    StreamPath     string
    OutputID       string
    RemoteURL      string
    StreamKey      string
    VideoArgs      []string
    AudioArgs      []string
    PlatformPreset string
    FFmpegOptions  map[string]string
    Status         OutputStatus
    LastError      string
    PID            int
}
```

### Recording Entry

Filesystem-backed recordings are returned as:

```go
type recordingEntry struct {
    StreamPath string    `json:"stream_path"`
    Name       string    `json:"name"`
    Filename   string    `json:"filename"`
    StartedAt  time.Time `json:"started_at"`
    FileSize   int64     `json:"file_size"`
    Active     bool      `json:"active"`
}
```

### Stats Response

```go
type statsResponse struct {
    Server  serverStats   `json:"server"`
    Inputs  []inputStats  `json:"inputs"`
    Outputs []outputStats `json:"outputs"`
}
```

`inputStats` includes `remote_addr` for push publishers and optional per-process telemetry.  
`outputStats` includes current status, error state, and optional per-process telemetry.

The `/stats` response is served from a background-refreshed in-memory snapshot. The handler does not probe processes or rebuild JSON on the request path; worker telemetry and self usage are refreshed asynchronously and the latest precomputed payload is returned.

For deeper runtime behavior and optimization notes, see `docs/state-and-stats.md`.

---

## HTTP API Endpoints

## Inputs

#### POST `/inputs`

Register an input.

Use `remote_url` for a pull input, or leave it empty to register an accept-mode push input.

**Request**

```json
{
  "stream_path": "pull-stream",
  "remote_url": "rtmp://qa-harness:1935/live/testsrc",
  "ingest_token": ""
}
```

**Response**

```json
{
  "status": "ok",
  "stream_path": "pull-stream"
}
```

#### GET `/inputs`

List registered inputs from the state store.

**Response**

```json
[
  {
    "stream_path": "pull-stream",
    "mode": "Pull",
    "status": "Active",
    "remote_url": "rtmp://qa-harness:1935/live/testsrc"
  },
  {
    "stream_path": "push-stream",
    "mode": "Accept",
    "status": "Active",
    "remote_addr": "172.18.0.5:49412"
  }
]
```

#### DELETE `/inputs?stream={stream_path}`

Delete an input and stop any attached outputs, active recording, and active HLS generation first.

**Response**

```json
{
  "status": "ok"
}
```

## Outputs

#### POST `/outputs`

Create and immediately start an output.

**Request**

```json
{
  "stream_path": "pull-stream",
  "output_id": "youtube-main",
  "remote_url": "rtmp://example.com/live/app",
  "preset": "YouTube",
  "video_codec": "libx264",
  "audio_codec": "aac",
  "resolution": "1920x1080",
  "framerate": "30",
  "bitrate": "4500k",
  "rotation": "transpose=2"
}
```

`preset` and explicit ffmpeg option fields can be combined. Explicit values override preset defaults.

**Response**

```json
{
  "status": "ok",
  "output_id": "youtube-main"
}
```

#### GET `/outputs`

List all outputs.

#### GET `/outputs?stream={stream_path}`

List outputs for a single input.

#### POST `/outputs/start`

Restart an existing output in place.

This is the row-level Start behavior in the Web UI. For pull inputs, the backend ensures the input is active before starting the output. For accept-mode push inputs, it restarts only the output worker and relies on the publisher to connect to the hub.

If the output is already running, the endpoint returns `200 OK` without launching a duplicate worker. During async import rebuilds, output start requests may also observe a startup reservation that suppresses duplicate launches while the imported worker is still being created.

**Request**

```json
{
  "stream_path": "push-stream",
  "output_id": "backup"
}
```

**Response**

```json
{
  "status": "ok",
  "output_id": "backup"
}
```

#### POST `/outputs/stop`

Stop an existing output without deleting its definition.

**Request**

```json
{
  "stream_path": "push-stream",
  "output_id": "backup"
}
```

**Response**

```json
{
  "status": "ok",
  "output_id": "backup"
}
```

#### DELETE `/outputs?stream={stream_path}&id={output_id}`

Delete an output definition entirely.

**Response**

```json
{
  "status": "ok"
}
```

## Recording

#### POST `/record?stream={stream_path}`

Start recording for an input.

**Response**

```json
{
  "status": "ok",
  "stream_path": "pull-stream"
}
```

#### DELETE `/record?stream={stream_path}`

Stop an active recording.

**Response**

```json
{
  "status": "ok"
}
```

## Recordings

#### GET `/recordings`

List recordings from the filesystem, with `active` overlaid from current runtime state.

The API does not keep historical recording state in memory. The disk directory is the source of truth.

**Response**

```json
[
  {
    "stream_path": "pull-stream",
    "name": "pull-stream",
    "filename": "pull-stream_2026-04-04_13-15-20.mp4",
    "started_at": "2026-04-04T07:45:20Z",
    "file_size": 10485760,
    "active": false
  }
]
```

#### DELETE `/recordings?filename={relative_filename}`

Delete a completed recording file.

The request is rejected if the file is still active.

**Response**

```json
{
  "status": "ok"
}
```

#### GET `/recordings/sse`

Server-Sent Events endpoint for recording list refresh.

This route is backed by an API-layer `fsnotify` watcher and broadcasts lightweight refresh signals when files are created, written, removed, or renamed under the recordings directory.

**Headers**

- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `Connection: keep-alive`

**Stream format**

```text
retry: 3000

data: update
```

The Web UI uses this to refetch `/recordings` instead of embedding full recording lists inside SSE events.

#### GET `/recordings/{file}`

Serve a recording file from the configured recordings directory.

This is the path used by the browser download links in the Web UI.

## Stats

#### GET `/stats`

Return server, input, and output status plus optional process telemetry.

**Response**

```json
{
  "server": {
    "cpu": 1.3,
    "mem_mb": 42.7
  },
  "inputs": [
    {
      "stream_path": "push-stream",
      "mode": "Accept",
      "status": "Active",
      "remote_addr": "172.18.0.5:49412",
      "telemetry": {
        "cpu": 0.8,
        "mem_mb": 18.2,
        "speed": 1.0,
        "bitrate": 3500000
      }
    }
  ],
  "outputs": [
    {
      "stream_path": "push-stream",
      "output_id": "youtube-main",
      "remote_url": "rtmp://example.com/live/key",
      "status": "Active",
      "telemetry": {
        "cpu": 1.2,
        "mem_mb": 24.0,
        "speed": 1.0,
        "bitrate": 4200000
      }
    }
  ]
}
```

## Export and Import

#### GET `/system/export`

Export the current runtime config as `relay_config.json`.

The response is served as:

- `Content-Type: application/json`
- `Content-Disposition: attachment; filename="relay_config.json"`

#### POST `/system/import`

Import a `relay_config.json`-compatible payload.

The endpoint responds immediately with `202 Accepted` and performs the rebuild asynchronously. The current runtime workers are drained first, imported inputs are re-registered, pull inputs are waited back to `Active`, and then imported outputs are started.

Because import is asynchronous, clients that immediately issue follow-up `/outputs/start` calls may briefly race output definition creation and should retry on `not found` responses.

**Response**

```json
{
  "status": "accepted"
}
```

## Presets

#### GET `/presets`

Return available output presets from the state package.

**Response**

```json
{
  "YouTube": {
    "video_args": ["-c:v", "libx264", "-preset", "veryfast", "-b:v", "4500k"],
    "audio_args": ["-c:a", "aac", "-b:a", "128k"]
  }
}
```

## HLS

#### POST `/hls/start?stream={stream_path}`

Start or attach to an HLS viewer session for a stream.

**Response**

```json
{
  "status": "ok",
  "stream_path": "pull-stream",
  "viewer_id": "9dcb4f4f-bb89-4b36-8d8a-2b9d77962f04",
  "playlist_url": "/hls/pull-stream/index.m3u8"
}
```

#### POST `/hls/stop`

Stop an HLS viewer session.

**Request**

```json
{
  "stream": "pull-stream",
  "viewer_id": "9dcb4f4f-bb89-4b36-8d8a-2b9d77962f04"
}
```

**Response**

```json
{
  "status": "ok"
}
```

#### POST `/hls/heartbeat`

Refresh a viewer session heartbeat.

**Request**

```json
{
  "stream": "pull-stream",
  "viewer_id": "9dcb4f4f-bb89-4b36-8d8a-2b9d77962f04"
}
```

**Success response**

```json
{
  "status": "ok"
}
```

**Expired viewer response**

`410 Gone`

```json
{
  "error": "viewer session expired or stream ended"
}
```

#### GET `/hls/{stream_path}/index.m3u8`

Serve the HLS playlist for a stream.

#### GET `/hls/{stream_path}/{segment}`

Serve HLS segment files generated by the HLS manager.

---

## Export and Import Format

The export/import payload matches `relay_config.json`.

```json
[
  {
    "input_url": "rtmp://localhost:1933/live/stream",
    "input_name": "Tamil",
    "outputs": [
      {
        "output_url": "rtmp://localhost:1935/live/stream",
        "output_name": "TN-2",
        "platform_preset": "Instagram"
      },
      {
        "output_url": "rtmp://localhost:1936/live/stream",
        "output_name": "TN-1",
        "ffmpeg_options": {
          "video_codec": "",
          "audio_codec": "aac",
          "resolution": "1280x720",
          "framerate": "60",
          "bitrate": "2000k",
          "rotation": ""
        }
      }
    ]
  }
]
```

Notes:

- `input_url: ""` means accept-mode push ingest.
- `platform_preset` and `ffmpeg_options` both round-trip through export/import.
- `ffmpeg_options` keys currently used are:
  - `video_codec`
  - `audio_codec`
  - `resolution`
  - `framerate`
  - `bitrate`
  - `rotation`

---

## Presets

Presets are defined in the state package and converted into ffmpeg args for outputs.

Explicit request fields such as `bitrate` or `resolution` override preset defaults when both are provided.

---

## State and Stats Behavior

This section summarizes operational behavior that is important for contributors.

### State store keying

- Inputs are keyed by `stream_path`
- Outputs are keyed by composite `stream_path/output_id`
- Active recordings are keyed by `stream_path`
- Telemetry is keyed by process PID

### What is usually cheap

Most runtime updates are point mutations on a single key (status updates, PID updates,
telemetry updates, viewer heartbeats), not whole-store rewrites.

### What still scans

- `/stats` cache refresh reads all inputs and outputs every `500ms`
- `DELETE /inputs` scans outputs to remove those under one stream path
- `GET /system/export` and `POST /system/import` iterate full input/output sets
- `DELETE /recordings` checks active recordings by filename against active runtime set

These scans are currently acceptable because they are either background aggregation
or low-frequency admin operations.

### Request-path behavior for `/stats`

- Handler serves pre-encoded cached JSON
- No on-demand process probing in request handler
- No on-demand stats object reconstruction in request handler

This keeps polling latency and lock pressure stable under concurrency.
