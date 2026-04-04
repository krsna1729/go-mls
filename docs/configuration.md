# Configuration Guide

Go-MLS is configured via a JSON file (default: `config.json`).

## Configuration Structure

The configuration is divided into several sections:

- **http**: HTTP API server settings.
- **relay**: Hub configuration (RTMP/RTSP), timeouts.
- **recording**: Recording settings.
- **hls**: HLS streaming settings.
- **ffmpeg**: FFmpeg process settings.
- **logging**: Application logging.

## Duration Format
All duration fields (e.g., `read_timeout`, `input_timeout`) use Go's duration string format.
Examples: `"30s"`, `"5m"`, `"1h"`, `"500ms"`.

## Complete Configuration Reference

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
    "hub_type": "rtmp",
    "rtmp_hub": {
      "host": "0.0.0.0",
      "port": 1935
    },
    "rtsp_server": {
      "host": "0.0.0.0",
      "port": 8554
    }
  },
  "recording": {
    "directory": "recordings"
  },
  "hls": {
    "cleanup_interval": "2m",
    "session_timeout": "5m",
    "failed_cooldown": "30s",
    "not_found_log_interval": "10s",
    "playlist_ready_timeout": "10s",
    "playlist_poll_interval": "200ms",
    "playlist_poll_attempts": 50,
    "viewer_heartbeat_timeout": "30s",
    "ffmpeg_stop_timeout": "2s",
    "playlist_base_dir": "/tmp",
    "segment_duration": "2s",
    "playlist_size": 6,
    "ffmpeg_preset": "ultrafast"
  },
  "ffmpeg": {
    "path": "ffmpeg",
    "loglevel": "info"
  },
  "logging": {
    "level": "info",
    "file": ""
  }
}
```

## Hub Configuration

### RTMP Hub (Default)
For OBS, streaming software, or any RTMP-compatible encoder:

```json
"relay": {
  "hub_type": "rtmp",
  "rtmp_hub": {
    "host": "0.0.0.0",
    "port": 1935
  }
}
```

### RTSP Hub
For IP cameras, NVRs, or RTSP-compatible sources:

```json
"relay": {
  "hub_type": "rtsp",
  "rtsp_server": {
    "host": "0.0.0.0",
    "port": 8554
  }
}
```

## Common Scenarios

### Enable Recording
To enable recording, ensure the `recording` section has a valid directory. Recordings are triggered via the API.

```json
"recording": {
  "directory": "./recordings"
}
```

### HLS Tuning
You can tune HLS for low latency or stability using `segment_duration`, `playlist_size`, and `ffmpeg_preset`.

**Low Latency (Target ~3-5s):**
```json
"hls": {
  "segment_duration": "1s",
  "playlist_size": 3,
  "ffmpeg_preset": "ultrafast",
  "playlist_poll_interval": "100ms",
  "playlist_ready_timeout": "5s"
}
```

**High Stability (Target ~15-20s):**
```json
"hls": {
  "segment_duration": "4s",
  "playlist_size": 10,
  "ffmpeg_preset": "veryfast"
}
```

### Debugging
To see detailed logs from both the app and FFmpeg:

```json
"logging": {
  "level": "debug"
},
"ffmpeg": {
  "loglevel": "debug"
}
```
