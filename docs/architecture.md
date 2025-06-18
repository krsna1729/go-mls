# Go-MLS System Architecture

## Overview

Go-MLS is a high-performance, scalable live streaming solution designed to handle multiple input sources and relay them to hundreds of destinations simultaneously. The system is built with a modular architecture that ensures reliability, performance, and maintainability.

## Core Components

### 1. Application Layer

```
┌─────────────────────────────────────────────────────────────┐
│                    Go-MLS Application                       │
├─────────────────────────────────────────────────────────────┤
│  HTTP Server  │  Web UI   │  REST API  │  Static Assets    │
├─────────────────────────────────────────────────────────────┤
│  Relay Manager │ Recording │ RTSP Server │ Process Monitor  │
│               │ Manager   │            │                   │
├─────────────────────────────────────────────────────────────┤
│  Logger       │ Config    │ HTTP Utils │ Status Monitor    │
│               │ Manager   │           │                   │
└─────────────────────────────────────────────────────────────┘
```

### 2. Stream Processing Pipeline

```
Input Sources ──▶ Input Relay ──▶ RTSP Server ──▶ Output Relays ──▶ Destinations
      │                │              │               │
      ▼                ▼              ▼               ▼
┌──────────┐  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐
│ RTMP     │  │ ffmpeg      │  │ MediaMTX     │  │ ffmpeg      │
│ RTSP     │  │ Input       │  │ RTSP         │  │ Output      │
│ HLS      │  │ Process     │  │ Relay        │  │ Processes   │
│ Files    │  │             │  │              │  │             │
└──────────┘  └─────────────┘  └──────────────┘  └─────────────┘
      │                │              │               │
      ▼                ▼              ▼               ▼
┌──────────┐  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐
│ External │  │ Stream      │  │ Local RTSP   │  │ Platform    │
│ Streams  │  │ Ingestion   │  │ Endpoint     │  │ Endpoints   │
└──────────┘  └─────────────┘  └──────────────┘  └─────────────┘
```

## Component Details

### Relay Manager (`internal/stream/relay_manager.go`)

The Relay Manager is the core orchestrator of the streaming pipeline:

**Key Responsibilities:**
- Manages input and output relay processes
- Handles ffmpeg process lifecycle
- Implements platform-specific presets
- Manages stream readiness detection
- Provides bitrate monitoring
- Handles configuration persistence

**Data Structures:**
```go
type RelayManager struct {
    inputs          map[string]*InputRelay
    endpoints       map[string]*RelayEndpoint
    rtspServer      *RTSPServer
    logger          *logger.Logger
    mu              sync.RWMutex
}

type RelayEndpoint struct {
    InputName       string
    OutputName      string
    InputURL        string
    OutputURL       string
    PlatformPreset  string
    FFmpegOptions   map[string]string
    Process         *exec.Cmd
    Status          string
    LastBitrate     string
}
```

### RTSP Server (`internal/stream/rtsp_server.go`)

Built on top of MediaMTX, provides local RTSP endpoints for stream distribution:

**Features:**
- Stream creation and management
- Client connection handling
- Stream readiness detection
- RTSP protocol implementation

**Key Methods:**
```go
func (rs *RTSPServer) CreateStream(path string) error
func (rs *RTSPServer) RemoveStream(path string) error
func (rs *RTSPServer) WaitForStreamReady(path string, timeout time.Duration) error
```

### Recording Manager (`internal/stream/recording_manager.go`)

Handles stream recording and playback functionality:

**Capabilities:**
- Real-time stream recording
- File-based stream playback
- Recording download API
- Automatic cleanup

### HTTP Server (`main.go`)

Provides REST API and web interface:

**API Endpoints:**
- `/api/start-relay` - Start output relay
- `/api/stop-relay` - Stop output relay
- `/api/start-recording` - Start recording
- `/api/stop-recording` - Stop recording
- `/api/status` - Get system status
- `/api/import-config` - Import configuration
- `/api/export-config` - Export configuration

## Data Flow

### 1. Input Stream Processing

```
External Source ──▶ Input Relay (ffmpeg) ──▶ RTSP Server ──▶ Internal Distribution
       │                    │                     │
       ▼                    ▼                     ▼
┌─────────────┐    ┌─────────────────┐    ┌─────────────────┐
│ RTMP/RTSP   │    │ Stream          │    │ Local RTSP      │
│ HLS/File    │    │ Normalization   │    │ rtsp://         │
│ Input       │    │ & Validation    │    │ 127.0.0.1:8554  │
└─────────────┘    └─────────────────┘    └─────────────────┘
```

### 2. Output Stream Distribution

```
RTSP Server ──▶ Output Relay 1 ──▶ Platform 1 (YouTube)
    │       ──▶ Output Relay 2 ──▶ Platform 2 (Instagram)
    │       ──▶ Output Relay 3 ──▶ Platform 3 (TikTok)
    └─────────▶ Recording Process ──▶ Local Storage
```

### 3. Stream Synchronization

The system ensures proper synchronization through:

1. **Reference Counting**: Tracks active consumers per input
2. **Stream Readiness**: Waits for RTSP stream availability
3. **Process Management**: Coordinates ffmpeg process lifecycle
4. **Status Tracking**: Monitors process states

## Process Management

### Input Relay Lifecycle

```
Start Request ──▶ Reference Count++ ──▶ Create RTSP Stream ──▶ Start ffmpeg
                                                │
                                                ▼
Stop Request ──▶ Reference Count-- ──▶ Check Count ──▶ Stop ffmpeg (if 0)
                                          │
                                          ▼
                                    Remove RTSP Stream
```

### Output Relay Lifecycle

```
Start Request ──▶ Validate Config ──▶ Wait for Input Ready ──▶ Start ffmpeg
                                                │
                                                ▼
Monitor Bitrate ◀── Parse Progress ◀── ffmpeg stdout
                                                │
                                                ▼
Stop Request ──▶ Terminate Process ──▶ Cleanup Resources
```

## Configuration Management

### Configuration Structure

```json
{
  "inputs": [
    {
      "name": "MainCamera",
      "url": "rtmp://source.example.com/live/stream"
    }
  ],
  "endpoints": [
    {
      "input_name": "MainCamera",
      "output_name": "YouTube",
      "input_url": "rtmp://source.example.com/live/stream",
      "output_url": "rtmp://a.rtmp.youtube.com/live2/KEY",
      "platform_preset": "youtube"
    }
  ]
}
```

### Platform Presets

```go
var platformPresets = map[string][]string{
    "youtube": {"-c:v", "libx264", "-preset", "veryfast", "-b:v", "6000k", 
                "-maxrate", "6000k", "-bufsize", "12000k", "-c:a", "aac", 
                "-b:a", "128k", "-f", "flv"},
    "instagram": {"-c:v", "libx264", "-preset", "veryfast", "-b:v", "3500k",
                  "-vf", "transpose=1", "-c:a", "aac", "-b:a", "128k", "-f", "flv"},
    "tiktok": {"-c:v", "libx264", "-preset", "veryfast", "-b:v", "2500k",
               "-vf", "transpose=1", "-c:a", "aac", "-b:a", "128k", "-f", "flv"},
}
```

## Monitoring and Observability

### Metrics Collection

The system collects various metrics:
- **Stream Metrics**: Bitrate, frame rate, resolution
- **Process Metrics**: CPU usage, memory consumption
- **System Metrics**: Active streams, error counts
- **Performance Metrics**: Latency, throughput

### Logging Framework

Structured logging with levels:
- **DEBUG**: Detailed operation logs
- **INFO**: General operation status
- **WARN**: Warning conditions
- **ERROR**: Error conditions

### Health Checks

Continuous monitoring of:
- ffmpeg process health
- RTSP server status
- Stream availability
- Resource utilization

## Scalability Considerations

### Horizontal Scaling

- **Input Distribution**: Multiple instances can handle different input sources
- **Output Scaling**: Each instance can handle hundreds of output relays
- **Load Balancing**: Use load balancers for API traffic

### Vertical Scaling

- **CPU Optimization**: ffmpeg process management
- **Memory Management**: Stream buffer optimization
- **I/O Optimization**: Efficient file handling

### Performance Optimizations

- **Parallel Processing**: Concurrent relay startup
- **Stream Readiness**: Polling-based detection
- **Process Reuse**: Efficient ffmpeg lifecycle management
- **Configuration Caching**: In-memory config storage

## Security Considerations

### Input Validation
- URL validation and sanitization
- Configuration parameter validation
- Process argument sanitization

### Process Isolation
- ffmpeg process sandboxing
- Resource limit enforcement
- Error boundary implementation

### API Security
- Input validation
- Rate limiting capability
- Authentication hooks (extensible)
