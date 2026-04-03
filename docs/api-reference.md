# Go-MLS API Reference

**Generated**: April 2026  
**Status**: Current Architecture

---

## Table of Contents

1. [Pipeline](#pipeline)
2. [FFmpegFactory](#ffmpegfactory)
3. [FFmpegProcess](#ffmpegprocess)
4. [SSEBroker](#ssebroker)
5. [RTSPServerManager](#rtspservermanager)
6. [Types](#types)
7. [HTTP API Endpoints](#http-api-endpoints)
8. [Presets](#presets)

---

## Pipeline

The central manager for all streaming operations.

### Constructor

```go
func NewPipeline(l *logger.Logger, recDir string, ffmpegTimeout time.Duration) *Pipeline
```

### Configuration

```go
func (p *Pipeline) SetRTSPServer(srv *RTSPServerManager)
func (p *Pipeline) SetFFmpegFactory(factory FFmpegFactory)
func (p *Pipeline) GetRecDir() string
```

### Input Operations

```go
func (p *Pipeline) StartInput(ctx context.Context, name, sourceURL string) error
func (p *Pipeline) StopInput(name string) error
func (p *Pipeline) DeleteInput(inputName string) error
func (p *Pipeline) GetInputURL(name string) (string, bool)
```

### Output Operations

```go
func (p *Pipeline) StartOutput(ctx context.Context, name, inputName, destURL string, opts FFmpegOpts, preset string) error
func (p *Pipeline) StopOutput(name string) error
```

### Recording Operations

```go
func (p *Pipeline) StartRecording(ctx context.Context, name, inputName string) error
func (p *Pipeline) StopRecording(name string) error
func (p *Pipeline) ListRecordings() []*PipelineRecording
func (p *Pipeline) DeleteRecording(name string) error
```

### HLS Operations

```go
func (p *Pipeline) StartHLS(ctx context.Context, name, inputName string, preset string) error
func (p *Pipeline) StopHLS(name string) error
```

### Status & Config

```go
func (p *Pipeline) Status() PipelineStatus
func (p *Pipeline) ExportConfig(filename string) error
func (p *Pipeline) ImportConfig(filename string) error
```

### Lifecycle

```go
func (p *Pipeline) Shutdown()
```

---

## FFmpegFactory

Interface for creating FFmpeg processes. Default implementation uses real ffmpeg binaries.

### Interface

```go
type FFmpegFactory interface {
    NewInputProcess(ctx context.Context, src, dst string) (FFmpegProcess, error)
    NewOutputProcess(ctx context.Context, src, dst string, opts FFmpegOpts) (FFmpegProcess, error)
    NewHLSProcess(ctx context.Context, src, dir string, preset string) (FFmpegProcess, error)
    NewRecordProcess(ctx context.Context, src, file string) (FFmpegProcess, error)
}
```

### Default Implementation

```go
func NewDefaultFFmpegFactory() FFmpegFactory
```

### FFmpegOpts

```go
type FFmpegOpts struct {
    VideoCodec string   // e.g., "libx264", "copy"
    AudioCodec string   // e.g., "aac", "copy"
    Resolution string   // e.g., "1920x1080", "1280x720"
    Framerate  string  // e.g., "30", "60"
    Bitrate    string  // e.g., "4500k", "2500k"
    ExtraArgs  []string // Additional ffmpeg arguments
}
```

---

## FFmpegProcess

Interface for managing individual FFmpeg processes.

### Interface

```go
type FFmpegProcess interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context, timeout time.Duration) error
    Wait() error
    GetOutput() string
    GetSpeed() (float64, time.Time)
    GetBitrate() (float64, bool)
    GetPID() int
    OutputChannel() <-chan string
}
```

### Constructor

```go
func NewFFmpegProcess(ctx context.Context, args ...string) (FFmpegProcess, error)
```

---

## SSEBroker

Server-Sent Events broker for real-time updates.

### Constructor

```go
func NewSSEBroker() *SSEBroker
```

### Methods

```go
func (b *SSEBroker) Broadcast(msg string)
func (b *SSEBroker) Subscribe() (<-chan string, func())
func (b *SSEBroker) Handler() http.HandlerFunc
func (b *SSEBroker) Shutdown()
```

---

## RTSPServerManager

Manages the embedded RTSP server.

### Constructor

```go
func NewRTSPServerManager(l *logger.Logger, host string, port int) *RTSPServerManager
```

### Lifecycle

```go
func (rm *RTSPServerManager) Start() error
func (rm *RTSPServerManager) Stop()
```

### Stream Management

```go
func (rm *RTSPServerManager) AddStream(path string) (*gortsplib.ServerStream, error)
func (rm *RTSPServerManager) RemoveStream(path string)
func (rm *RTSPServerManager) GetStreamStats() map[string]StreamStats
func (rm *RTSPServerManager) GetRTSPURL(path string) string
func (rm *RTSPServerManager) WaitForStreamReady(name string, timeout time.Duration) error
func (rm *RTSPServerManager) IsStreamReady(name string) bool
```

---

## SSEBroker

### Constructor

```go
func NewSSEBroker() *SSEBroker
func NewSSEBrokerWithWatcher(dir string) (*SSEBroker, error)
```

### Methods

```go
func (b *SSEBroker) Broadcast(msg string)
func (b *SSEBroker) BroadcastRefresh()
func (b *SSEBroker) Subscribe() (<-chan string, func())
func (b *SSEBroker) Handler() http.HandlerFunc
func (b *SSEBroker) Shutdown()
```

### RecordingInfo

```go
type RecordingInfo struct {
    Filename        string `json:"filename"`
    Size            int64  `json:"size"`
    ModifiedAtUnix  int64  `json:"modified_at_unix"`
}
```

---

## Types

### Relay

Internal relay structure grouping an input with its outputs, recording, and HLS session.

```go
type Relay struct {
    Input      *PipelineStream
    Outputs    map[string]*PipelineStream
    Recording  *PipelineRecording
    HLSSession *PipelineHLSSession
}
```

### StatusResponse

Hierarchical status response with relays grouped by input.

```go
type StatusResponse struct {
    Server PipelineServerStatus `json:"server"`
    Relays []RelayStatus        `json:"relays"`
}

type RelayStatus struct {
    Input   RelayInputStatus    `json:"input"`
    Outputs []RelayOutputStatus `json:"outputs"`
}

type RelayInputStatus struct {
    InputName       string  `json:"input_name"`
    InputURL        string  `json:"input_url"`
    LocalURL        string  `json:"local_url"`
    Status          string  `json:"status"`
    LastError       string  `json:"last_error,omitempty"`
    RefCount        int     `json:"ref_count"`
    Speed           float64 `json:"speed"`
    CPU             float64 `json:"cpu"`
    Mem             uint64  `json:"mem"`
    RecordingActive bool    `json:"recording_active"`
}

type RelayOutputStatus struct {
    OutputName string  `json:"output_name"`
    Status     string  `json:"status"`
    LastError  string  `json:"last_error,omitempty"`
    Preset     string  `json:"preset,omitempty"`
    Bitrate    float64 `json:"bitrate"`
    CPU        float64 `json:"cpu"`
    Mem        uint64  `json:"mem"`
}

type PipelineServerStatus struct {
    CPU float64 `json:"cpu"`
    Mem uint64  `json:"mem"`
}
```

### PipelineStream

```go
type PipelineStream struct {
    Name      string
    Type      PipelineStreamType  // PTypeInput, PTypeOutput
    SourceURL string
    LocalURL  string
    Status    PipelineStreamStatus  // PStreamStopped, PStreamStarting, PStreamRunning, PStreamError
    LastError string
    Proc      FFmpegProcess
    RefCount  int
    CreatedAt time.Time
    StartedAt time.Time
}
```

### PipelineStreamStatus

```go
const (
    PStreamStopped PipelineStreamStatus = iota
    PStreamStarting
    PStreamRunning
    PStreamError
)

func (s PipelineStreamStatus) String() string
```

### PipelineHLSSession

```go
type PipelineHLSSession struct {
    PipelineStream
    Mu          sync.RWMutex
    Dir         string
    Ready       bool
    ViewerIDs   map[string]time.Time
    LastAccess  time.Time
    ViewerCount int
}
```

### PipelineRecording

```go
type PipelineRecording struct {
    Name      string
    SourceURL string
    Filename  string
    FilePath  string
    FileSize  int64
    StartedAt time.Time
    StoppedAt time.Time
    Active    bool
}
```

### RecordingListItem

```go
type RecordingListItem struct {
    Name      string    `json:"name"`
    Source    string    `json:"source"`
    Filename  string    `json:"filename"`
    StartedAt time.Time `json:"started_at"`
    FileSize  int64     `json:"file_size"`
    Active    bool      `json:"active"`
}
```

---

## HTTP API Endpoints

### Relay Endpoints

#### POST `/api/relay/start`

Start an output relay. Auto-starts input if not running.

**Request:**
```json
{
  "input_name": "MyInput",
  "input_url": "rtsp://source:554/stream",
  "output_name": "MyOutput",
  "output_url": "rtmp://dest/live/key",
  "platform_preset": "YouTube",
  "ffmpeg_options": {
    "resolution": "1920x1080",
    "bitrate": "4500k"
  }
}
```

**Response:** `200 OK`
```json
{"status": "started"}
```

---

#### POST `/api/relay/stop`

Stop an output relay.

**Request:**
```json
{
  "output_name": "MyOutput"
}
```

**Response:** `200 OK`
```json
{"status": "stopped"}
```

---

#### GET `/api/relay/status`

Get status of all relays (hierarchical by input).

**Response:** `200 OK`
```json
{
  "server": {"cpu": 12.5, "mem": 52428800},
  "relays": [
    {
      "input": {
        "input_name": "MyInput",
        "input_url": "rtsp://source:554/stream",
        "local_url": "rtsp://localhost:8554/relay/MyInput",
        "status": "running",
        "ref_count": 2,
        "recording_active": true
      },
      "outputs": [
        {
          "output_name": "MyOutput",
          "status": "running",
          "preset": "YouTube",
          "bitrate": 4500.0
        }
      ]
    }
  ]
}
```

---

#### POST `/api/relay/delete-input`

Force delete an input and all its consumers.

**Request:**
```json
{
  "input_name": "MyInput"
}
```

**Response:** `200 OK`
```json
{"status": "deleted"}
```

---

#### POST `/api/relay/delete-output`

Stop and delete an output.

**Request:**
```json
{
  "output_name": "MyOutput"
}
```

**Response:** `200 OK`
```json
{"status": "deleted"}
```

---

#### GET `/api/relay/presets`

Get available platform presets.

**Response:** `200 OK`
```json
{
  "YouTube": {
    "name": "YouTube",
    "options": {
      "video_codec": "libx264",
      "audio_codec": "aac",
      "resolution": "1920x1080",
      "framerate": "30",
      "bitrate": "4500k"
    }
  },
  "Facebook": {...},
  "Twitch": {...}
}
```

---

#### POST `/api/relay/export`

Export current configuration to JSON file.

**Response:** `200 OK` (file download)

---

#### POST `/api/relay/import`

Import configuration from JSON file upload.

**Request:** `multipart/form-data` with `file` field

**Response:** `200 OK`
```json
{"status": "imported"}
```

---

### Recording Endpoints

#### POST `/api/recording/start`

Start recording an input.

**Request:**
```json
{
  "name": "MyRecording",
  "input_name": "MyInput",
  "input_url": "rtsp://source:554/stream"
}
```

**Response:** `200 OK`
```json
{"status": "started"}
```

---

#### POST `/api/recording/stop`

Stop a recording.

**Request:**
```json
{
  "name": "MyRecording"
}
```

**Response:** `200 OK`
```json
{"status": "stopped"}
```

---

#### GET `/api/recording/list`

List all recordings.

**Response:** `200 OK`
```json
[
  {
    "name": "MyRecording",
    "filename": "MyRecording_1234567890.mp4",
    "file_size": 104857600,
    "started_at": "2026-04-03T10:00:00Z",
    "stopped_at": "2026-04-03T11:00:00Z",
    "active": false
  }
]
```

---

#### POST `/api/recording/delete`

Delete a recording file.

**Request:**
```json
{
  "filename": "MyRecording_1234567890.mp4"
}
```

**Response:** `200 OK`
```json
{"status": "deleted"}
```

---

#### GET `/api/recording/download`

Download a recording file.

**Query Parameters:**
- `filename` (required): Name of the recording file

**Response:** `200 OK` (file download)

---

#### GET `/api/recording/sse`

Server-Sent Events for recording updates. Watches the recordings directory and sends full list on changes.

**Response:** `200 OK` (text/event-stream)

**Event Format:**
```json
data: [{"filename":"rec_123.mp4","size":1048576,"modified_at_unix":1709500000}]
```

**Triggers:**
- `fsnotify.Write` - File modified
- `fsnotify.Create` - New file created
- `fsnotify.Remove` - File deleted

---

### HLS Endpoints

#### POST `/api/relay/hls/start-viewer`

Start an HLS viewer session.

**Request:**
```json
{
  "input_name": "MyInput"
}
```

**Response:** `200 OK`
```json
{
  "viewer_id": "viewer-1234567890",
  "playlist_url": "/api/relay/watch-input/hls/MyInput/index.m3u8"
}
```

---

#### POST `/api/relay/hls/stop-viewer`

Stop an HLS viewer session.

**Request:**
```json
{
  "input_name": "MyInput",
  "viewer_id": "viewer-1234567890"
}
```

**Response:** `200 OK`
```json
{"status": "stopped"}
```

---

#### POST `/api/relay/hls/heartbeat`

Send viewer heartbeat to keep session alive.

**Request:**
```json
{
  "input_name": "MyInput",
  "viewer_id": "viewer-1234567890"
}
```

**Response:** `200 OK`
```json
{"status": "ok"}
```

---

#### GET `/api/relay/watch-input/hls/{inputName}/{file}`

Serve HLS playlist and segment files.

**Path Parameters:**
- `inputName`: Name of the HLS session
- `file`: Either `index.m3u8` or a `.ts` segment file

**Response:**
- `index.m3u8`: `Content-Type: application/vnd.apple.mpegurl`
- `.ts` segments: `Content-Type: video/MP2T`

---

### RTSP Endpoints

#### GET `/api/rtsp/status`

Get RTSP server stream statistics.

**Response:** `200 OK`
```json
{
  "streams": {
    "relay/MyInput": {
      "name": "MyInput",
      "path": "relay/MyInput",
      "client_count": 2,
      "bytes_received": 1048576,
      "start_time": "2026-04-03T10:00:00Z"
    }
  },
  "total": 1
}
```

---

## Presets

### ApplyPresetAndOptions

```go
func ApplyPresetAndOptions(preset string, manualOpts map[string]string) FFmpegOpts
```

Combines a platform preset with manual override options. Manual options take precedence.

**Example:**
```go
opts := ApplyPresetAndOptions("YouTube", map[string]string{
    "resolution": "1280x720",
})
// Result: YouTube preset with 1280x720 override
```

### PlatformPresets

```go
var PlatformPresets = map[string]PlatformPreset{
    "YouTube": {
        Name: "YouTube",
        Options: FFmpegOpts{
            VideoCodec: "libx264",
            AudioCodec: "aac",
            Resolution: "1920x1080",
            Framerate:  "30",
            Bitrate:    "4500k",
        },
    },
    "Facebook": {...},
    "Twitch": {...},
    "Instagram": {...},
    "Custom": {Name: "Custom", Options: FFmpegOpts{}},
}
```

---

## Related Documentation

- [Architecture Overview](architecture.md) - High-level architecture with diagrams
- [Configuration](configuration.md) - JSON config schema
