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
# Run consolidated harness (go-mls + qa-harness) and exit with harness status
docker compose down --remove-orphans
docker compose up --build --abort-on-container-exit --exit-code-from qa-harness qa-harness

# Verify recordings
ls -la recordings/*stream_*.mp4

# Verify HLS files
curl http://localhost:8080/hls/pull-stream/index.m3u8 | head -10

# Tear down
docker compose down -v
```

### Interactive E2E Helpers

You can run the harness container interactively and source the script functions.

```bash
# Start go-mls only
docker compose up -d go-mls

# Enter qa-harness shell
docker compose run --rm --entrypoint /bin/sh qa-harness

# Inside qa-harness shell (POSIX sh)
E2E_SOURCE_ONLY=1 . /e2e-test.sh
# In bash shells, plain source also works:
# . /e2e-test.sh
e2e_prepare_harness
register_pull_input pull-stream "$SOURCE_RTMP_FOR_GOMLS"
register_push_input push-stream
ensure_push_input_active push-stream
create_output pull-stream demo-pull "$OUTPUT_RTMP_FOR_GOMLS/live/demo-pull" /results/demo_pull.json
verify_stream_active "$OUTPUT_RTMP_LOCAL/live/demo-pull" 20 demo-pull
```

Useful functions for interactive testing:
- `e2e_prepare_harness`
- `register_pull_input`, `register_push_input`, `list_inputs`
- `create_output`, `start_output`, `list_outputs`
- `verify_stream_active`, `verify_profile_exact`, `verify_profile_dims_any_order`
- `start_recording`, `start_hls_viewer`, `stop_hls_viewer`, `export_config`, `import_config`

### Partial Harness Setup for Manual UI/Import Testing

When you only want sources running (without full scripted e2e phases), start `go-mls`, keep a persistent `qa-harness` shell alive, and run source helpers only.

```bash
# Host shell: start go-mls service
docker compose up -d go-mls

# Host shell: run persistent qa-harness shell
docker compose run --rm --entrypoint /bin/sh qa-harness
```

Inside the `qa-harness` shell:

```sh
# Prevent auto-running e2e_main when sourcing
E2E_SOURCE_ONLY=1 . /e2e-test.sh

# Start local harness RTMP and source publishers only
start_local_rtmp_server
start_pull_source_publisher      # publishes to rtmp://127.0.0.1:1935/live/testsrc
start_push_source                # publishes to rtmp://go-mls:1935/push-stream

# Keep shell alive while testing from browser/API
tail -f /dev/null
```

From the host, run a manual import and verify UI behavior:

```bash
curl -sS -X POST \
  -H 'Content-Type: application/json' \
  --data-binary @relay_config.json \
  http://localhost:8080/system/import

# Optional quick API verification
curl -sS http://localhost:8080/stats | head -c 1200
```

Notes:
- This path is useful for import/UI checks because it avoids running all e2e phases.
- `qa-harness` must stay running for pull/push publishers to remain active.
- If re-sourcing helper functions repeatedly, `start_local_rtmp_server` can log port-in-use warnings if nginx is already running.

The e2e tests verify:
- Pull ingest from local RTMP source within `qa-harness`
- Push ingest from local FFmpeg publisher within `qa-harness`
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
