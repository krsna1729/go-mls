# Go-MLS: Go Media Live Streamer

Go-MLS is a Go-based service for live video relay, recording, and monitoring, with a web UI for control and observability. It is designed for multi-source, multi-destination streaming, with dynamic relay management and recording support.

**Architecture**: Uses a single `Pipeline` struct to manage all streams—no interfaces, no callbacks, no global state.

## Features
- Relay multiple input streams to multiple output destinations (RTMP/RTSP)
- Dynamic add/remove/update of relays and endpoints via web UI/API
- Real-time relay/server status and statistics (CPU, memory, bitrate)
- Recording of any input stream to disk, with browser download and delete
- Web-based UI for control, search, and monitoring
- HLS streaming for browser playback
- All backend logic in Go, frontend is static HTML/JS/CSS

## Getting Started

### Prerequisites
- Go 1.18 or newer
- ffmpeg installed and available in your PATH

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
    "path": "ffmpeg",                        // Path to ffmpeg binary used for all streaming/recording
    "loglevel": "info"                       // ffmpeg loglevel (e.g. info, warning, error)
  },
  "hls": {
    "cleanup_interval": "2m",               // How often to clean up old HLS temp dirs
    "session_timeout": "5m",                // How long to keep an HLS session alive without viewers
    "viewer_heartbeat_timeout": "30s",      // Viewer considered disconnected after this
    "playlist_ready_timeout": "30s",        // Max time to wait for playlist to appear (fsnotify+poll)
    "playlist_base_dir": "/tmp"             // Base directory for HLS playlist temp dirs (default: /tmp)
  }
}
```
Advanced HLS options (rarely need to change, see code for defaults):

`failed_cooldown, not_found_log_interval, playlist_poll_interval, playlist_poll_attempts, ffmpeg_stop_timeout`

Run with custom configuration:
```bash
./go-mls -config config.json
```

### Command Line Options
- `-config`: Path to configuration file (optional)

### Usage
- Access the web UI at `http://localhost:8080`
- Add/edit relay endpoints (input/output pairs) via the web interface
- Export/Import configuration of all relays
- Start, stop, and update relays in real time
- Start/stop recordings and download completed files
- View relay/server status and statistics

---

## Documentation

- [Architecture Overview](docs/architecture.md) - High-level architecture with diagrams
- [API Reference](docs/api-reference.md) - Method signatures and HTTP endpoints
- [Configuration](docs/configuration.md) - JSON config schema

## Implementation

For implementation details, see `main.go` and `internal/stream/`.
