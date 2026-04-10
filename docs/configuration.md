# Configuration Guide

Go-MLS is configured via a JSON file (default: `config.json`).
The checked-in reference file is `config.example.json`, and examples below mirror that file.

## Configuration Structure

The configuration is divided into several sections:

- **http**: HTTP API server settings.
- **relay**: Hub configuration (RTMP/RTSP), SRT passive-accept listener settings, and timeouts.
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
    "rtsp_hub": {
      "host": "0.0.0.0",
      "port": 8554
    },
    "srt_hub": {
      "host": "0.0.0.0",
      "port": 9000
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
    "loglevel": "error"
  },
  "logging": {
    "level": "info",
    "file": ""
  }
}
```

## Hub Configuration

### Multi-Protocol Ingest Architecture

Go-MLS supports simultaneous ingest via multiple protocols through a **Composite Hub** architecture:

- All configured hub protocols are **started simultaneously**
- The `hub_type` setting designates which is the **primary** hub for Addr() reporting
- Each protocol operates independently but feeds the same internal RTMP backbone
- Inputs can be registered as pull-mode (external source) or accept-mode (wait for publisher)

### Hub Type: Primary Hub Selection

The `relay.hub_type` setting controls which hub is the **primary** (reports main address):

| Value | Primary | Secondary Hubs | When to Use |
|-------|---------|-----------------|----------|
| `"rtmp"` (default) | RTMP Hub | RTSP, SRT, Internal | OBS/streaming software push |
| `"rtsp"` | RTSP Hub | RTMP, SRT, Internal | IP cameras / NVR streams |
| `"srt"` | SRT Hub | RTMP, RTSP, Internal | SRT encoder inputs |

All hubs are started regardless of type; the primary determines what `Addr()` returns for API clients.

### RTMP Hub (Native Protocol Support)
For OBS, streaming software, encoders, or any RTMP-compatible publisher:

```json
"relay": {
  "hub_type": "rtmp",
  "rtmp_hub": {
    "host": "0.0.0.0",
    "port": 1935
  }
}
```

**Input modes:**
- **Accept RTMP**: Publishers push streams directly (e.g., `rtmp://server:1935/stream-path`)
- **Pull RTMP**: Register input with `pull_protocol: "rtmp"` and remote URL

### RTSP Hub (IP Camera & NVR Support)
For IP cameras, NVRs, and RTSP-compatible sources with automatic protocol bridging:

```json
"relay": {
  "hub_type": "rtsp",
  "rtsp_hub": {
    "host": "0.0.0.0",
    "port": 8554
  }
}
```

**Input modes:**
- **Accept RTSP**: Publishers push streams directly to hub listener
  - When a stream connects, an **RTSPAdapter** is spawned (FFmpeg bridge)
  - The adapter pulls from the RTSP hub and re-pushes to internal RTMP backbone
  - This allows RTSP sources to be treated as first-class inputs in the system
- **Pull RTSP**: Register input with `pull_protocol: "rtsp"` and remote URL
  - The Puller pulls directly from the remote RTSP source
  - Pushed to internal RTMP backbone

```json
{
  "stream_path": "camera-1",
  "accept_protocol": "rtsp",
  "token": ""
}
```

When a camera connects to `rtsp://server:8554/camera-1`, an RTSPAdapter is automatically started to bridge it.

### SRT Hub (SRT Accept-Mode Support)
For SRT caller publishers targeting passive-accept listeners:

```json
"relay": {
  "srt_hub": {
    "host": "0.0.0.0",
    "port": 9000
  }
}
```

**Input modes:**
- **Accept SRT**: Publishers send SRT calls, triggering **SRTAdapter** spawning
  - When SRT publisher connects, an SRTAdapter (FFmpeg bridge) is created
  - Adapter pulls from SRT listener and re-pushes to internal RTMP backbone
- **Pull SRT**: Register input with `pull_protocol: "srt"` and caller URL

```json
{
  "stream_path": "encoder-1",
  "accept_protocol": "srt"
}
```

**SRT Publisher Example:**
```bash
ffmpeg -i input.mp4 -c copy -f mpegts srt://server:9000?mode=caller&streamid=publish:encoder-1
```

### Internal RTMP Backbone (Automatic)
All protocols bridge to an internal RTMP hub for unified distribution:

```json
"relay": {
  "rtmp_hub": {
    "host": "127.0.0.1",
    "port": 1935
  }
}
```

- **Scope**: Localhost only (127.0.0.1), not exposed externally  
- **Purpose**: Internal convergence point for all ingested streams
- **Consumers**: Restreamer, Recorder, HLS Manager pull from this backbone
- **Auto-managed**: Created and started automatically via CompositeHub

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

## Note On Built-In Fallback Defaults
If `config.json` is missing, the app runs with built-in defaults from `internal/config/config.go`.
Those defaults mirror the values shown in `config.example.json`. Fields omitted from a partial `config.json` naturally retain their defaults; fields explicitly set in the JSON take precedence.

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
