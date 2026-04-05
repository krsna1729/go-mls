# Go-MLS: Go Media Live Streamer

Go-MLS is a Go-based service for live video relay, recording, and monitoring, with a web UI for control and observability. It is designed for multi-source, multi-destination streaming, with dynamic relay management and recording support.

## Features
- Relay multiple input streams to multiple output destinations (RTMP/RTSP)
- Dynamic add/remove/start/stop of inputs and outputs via web UI/API
- Real-time relay/server status and statistics (CPU, memory, bitrate)
- Recording of any input stream to disk, with browser download and delete
- Push ingest support with accept-mode inputs and runtime publisher remote address visibility
- Server-Sent Events for recording file updates in the web UI
- Web-based UI for control, search, and monitoring
- HLS streaming for browser playback
- All backend logic in Go, frontend is static HTML/JS/CSS

## Getting Started

### Prerequisites
- Go 1.21 or newer
- ffmpeg installed and available in your PATH
- Docker and Docker Compose (for e2e tests)

### Build and Run
```bash
git clone https://github.com/krsna/go-mls.git
cd go-mls
go build -o go-mls
./go-mls
```

### Configuration
Go-MLS uses a JSON configuration file for advanced settings. If no configuration file is provided, sensible defaults are used.

Create a `config.json` file (see `config.example.json` for reference):
```json
{
  "http": {
    "host": "0.0.0.0",
    "port": "8080",
    "read_timeout": "30s",
    "write_timeout": "30s",
    "idle_timeout": "120s"
  },
  "relay": {
    "input_timeout": "30s",
    "output_timeout": "60s",
    "rtsp_server": {
      "host": "127.0.0.1",
      "port": 8554
    }
  },
  "recording": {
    "directory": "recordings"
  },
  "logging": {
    "level": "info",
    "file": ""
  },
  "ffmpeg": {
    "path": "ffmpeg",
    "loglevel": "info"
  },
  "hls": {
    "viewer_heartbeat_timeout": "30s",
    "idle_timeout": "30s",
    "playlist_base_dir": "/tmp"
  }
}
```

Run with custom configuration:
```bash
./go-mls -config config.json
```

### Command Line Options
- `-config`: Path to configuration file (optional)

### Usage
- Access the web UI at `http://localhost:8080`
- Register pull inputs or accept-mode push inputs via the web interface
- Add outputs, stop/restart outputs in place, or delete them entirely
- Export/Import configuration of all relays using the `relay_config.json` format
- Start, stop, and update relays in real time
- Start/stop recordings, receive live recordings tab refreshes, and download completed files
- View relay/server status and statistics

---

## Testing

### Unit Tests
```bash
go test ./...
```

### Unit Tests with FFmpeg Tests
```bash
go test -short=false ./internal/worker/...
```

### End-to-End Tests (Docker Compose)
```bash
# Build and start all services
docker compose up --build -d

# Watch test progress
docker compose logs -f test-runner

# Check test results
docker compose logs test-runner | tail -20

# Verify recordings
ls -la recordings/*stream_*.mp4

# Verify HLS files
curl http://localhost:8080/hls/pull-stream/index.m3u8 | head -10

# Tear down
docker compose down -v
```

The e2e tests verify:
- Pull ingest from an RTMP source container (`source-rtmp`)
- Push ingest from an FFmpeg publisher container (`source-push`)
- Simultaneous operation with 2 inputs
- Multiple outputs per input (2 outputs each)
- Recording to disk
- Recording list refresh and file serving from the recordings directory
- HLS playlist generation
- HLS start/heartbeat/stop lifecycle
- Clean shutdown without goroutine leaks

---

## Documentation

- [Architecture Overview](docs/architecture.md) - High-level architecture with diagrams
- [API Reference](docs/api-reference.md) - Method signatures and HTTP endpoints
- [Configuration](docs/configuration.md) - JSON config schema
