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
    "viewer_heartbeat_timeout": "30s",
    "idle_timeout": "30s",
    "playlist_base_dir": "/tmp",
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
The HLS manager has a small, focused configuration surface:

- `viewer_heartbeat_timeout`: how long to keep a viewer session alive without heartbeat
- `idle_timeout`: how long to keep HLS ffmpeg running after viewer count reaches zero
- `playlist_base_dir`: filesystem location where playlists/segments are generated
- `ffmpeg_preset`: ffmpeg encoder preset used by HLS generation

**Fast reconnect UX (recommended for web UI playback):**
```json
"hls": {
  "viewer_heartbeat_timeout": "30s",
  "idle_timeout": "30s",
  "playlist_base_dir": "/tmp",
  "ffmpeg_preset": "ultrafast"
}
```

**Resource saving (stop HLS faster when idle):**
```json
"hls": {
  "viewer_heartbeat_timeout": "20s",
  "idle_timeout": "8s",
  "playlist_base_dir": "/tmp",
  "ffmpeg_preset": "ultrafast"
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
