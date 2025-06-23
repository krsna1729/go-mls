# Go-MLS: Go Media Live Streamer

Go-MLS is a Go-based service for live video relay, recording, and monitoring, with a web UI for control and observability. It is designed for multi-source, multi-destination streaming, with dynamic relay management and recording support.

## Features
- Relay multiple input streams to multiple output destinations (RTMP/RTSP)
- Dynamic add/remove/update of relays and endpoints via web UI/API
- Real-time relay/server status and statistics (CPU, memory, bitrate)
- Recording of any input stream to disk, with browser download and delete
- Web-based UI for control, search, and monitoring
- Prometheus metrics and Grafana dashboards for observability
- All backend logic in Go, frontend is static HTML/JS/CSS

## Getting Started

### Prerequisites
- Go 1.18 or newer
- ffmpeg installed and available in your PATH

### Build and Run
```sh
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
  }
}
```

Run with custom configuration:
```sh
./go-mls -config config.json
```

### Command Line Options
- `-recordings-dir`: Directory to store recordings (default: `./recordings`)
- `-config`: Path to configuration file (optional)

### Usage
- Access the web UI at `http://localhost:8080`
- Add/edit relay endpoints (input/output pairs) via the web interface
- Export/Import configuration of all relays
- Start, stop, and update relays in real time
- Start/stop recordings and download completed files
- View relay/server status and statistics

---

For implementation details, see `main.go` and `internal/stream/`.
