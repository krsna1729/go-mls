# Go-MLS Configuration Guide

This guide covers all configuration options and provides examples for different deployment scenarios.

---

## Configuration Methods

Go-MLS supports multiple configuration methods that can be used together:

1. **Command-line flags**: For basic server settings
2. **Environment variables**: For containerized deployments
3. **Configuration files**: For complex relay setups
4. **Runtime API**: For dynamic configuration changes

---

## Command-Line Flags

### Basic Server Configuration

```bash
./go-mls [flags]
```

**Available Flags:**
- `-port string`: HTTP server port (default: "8080")
- `-host string`: HTTP server host (default: "0.0.0.0")
- `-config string`: Path to configuration file (default: "relay_config.json")
- `-log-level string`: Log level: debug, info, warn, error (default: "info")
- `-rtsp-port string`: RTSP server port (default: "8554")
- `-rtsp-host string`: RTSP server host (default: "127.0.0.1")

**Examples:**
```bash
# Basic startup
./go-mls

# Custom port and host
./go-mls -port 9090 -host 127.0.0.1

# Debug logging with custom config
./go-mls -log-level debug -config /etc/go-mls/config.json

# Production settings
./go-mls -port 80 -host 0.0.0.0 -log-level warn
```

---

## Environment Variables

For containerized deployments, use environment variables:

```bash
# Server configuration
export GO_MLS_PORT=8080
export GO_MLS_HOST=0.0.0.0
export GO_MLS_LOG_LEVEL=info

# RTSP server configuration
export GO_MLS_RTSP_PORT=8554
export GO_MLS_RTSP_HOST=127.0.0.1

# Configuration file path
export GO_MLS_CONFIG_FILE=/app/config/relay_config.json

# Recording settings
export GO_MLS_RECORDINGS_DIR=/app/recordings
export GO_MLS_MAX_RECORDING_SIZE=10GB
```

**Docker Example:**
```dockerfile
ENV GO_MLS_PORT=8080
ENV GO_MLS_HOST=0.0.0.0
ENV GO_MLS_LOG_LEVEL=info
ENV GO_MLS_RECORDINGS_DIR=/recordings
```

---

## Configuration File Format

### Basic Structure

The configuration file (`relay_config.json`) defines inputs and outputs:

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
      "output_url": "rtmp://a.rtmp.youtube.com/live2/YOUR_STREAM_KEY",
      "platform_preset": "youtube"
    }
  ]
}
```

### Complete Configuration Example

```json
{
  "inputs": [
    {
      "name": "MainCamera",
      "url": "rtmp://rtmp.example.com/live/main"
    },
    {
      "name": "SecondaryCamera", 
      "url": "rtsp://camera.local:554/stream1"
    },
    {
      "name": "FileInput",
      "url": "/path/to/video.mp4"
    }
  ],
  "endpoints": [
    {
      "input_name": "MainCamera",
      "output_name": "YouTube-Main",
      "input_url": "rtmp://rtmp.example.com/live/main",
      "output_url": "rtmp://a.rtmp.youtube.com/live2/YOUR_KEY",
      "platform_preset": "youtube"
    },
    {
      "input_name": "MainCamera",
      "output_name": "Instagram-Vertical",
      "input_url": "rtmp://rtmp.example.com/live/main", 
      "output_url": "rtmp://live-api-s.facebook.com:80/rtmp/YOUR_KEY",
      "platform_preset": "instagram"
    },
    {
      "input_name": "MainCamera",
      "output_name": "Custom-Output",
      "input_url": "rtmp://rtmp.example.com/live/main",
      "output_url": "rtmp://custom.streaming.service/live/key",
      "ffmpeg_options": {
        "video_codec": "libx264",
        "preset": "medium",
        "bitrate": "4000k",
        "fps": "60",
        "resolution": "1280x720"
      }
    },
    {
      "input_name": "SecondaryCamera",
      "output_name": "TikTok-Live",
      "input_url": "rtsp://camera.local:554/stream1",
      "output_url": "rtmp://push.tiktokcdn.com/live/YOUR_KEY",
      "platform_preset": "tiktok"
    }
  ]
}
```

---

## Platform Presets

### Available Presets

Go-MLS includes optimized presets for popular streaming platforms:

#### YouTube (`youtube`)
```json
{
  "platform_preset": "youtube"
}
```
**Settings:**
- Resolution: 1920x1080
- Video Codec: H.264 (libx264)
- Video Bitrate: 6 Mbps
- Audio Codec: AAC
- Audio Bitrate: 128 kbps
- Preset: veryfast
- Format: FLV

#### Instagram (`instagram`)
```json
{
  "platform_preset": "instagram"
}
```
**Settings:**
- Resolution: 1080x1920 (vertical)
- Video Codec: H.264 (libx264)
- Video Bitrate: 3.5 Mbps
- Audio Codec: AAC
- Audio Bitrate: 128 kbps
- Transformation: Vertical rotation
- Format: FLV

#### TikTok (`tiktok`)
```json
{
  "platform_preset": "tiktok"
}
```
**Settings:**
- Resolution: 1080x1920 (vertical)
- Video Codec: H.264 (libx264)
- Video Bitrate: 2.5 Mbps
- Audio Codec: AAC
- Audio Bitrate: 128 kbps
- Transformation: Vertical rotation
- Format: FLV

#### Facebook (`facebook`)
```json
{
  "platform_preset": "facebook"
}
```
**Settings:**
- Resolution: 1280x720
- Video Codec: H.264 (libx264)
- Video Bitrate: 4 Mbps
- Audio Codec: AAC
- Audio Bitrate: 128 kbps
- Preset: veryfast
- Format: FLV

#### Twitch (`twitch`)
```json
{
  "platform_preset": "twitch"
}
```
**Settings:**
- Resolution: 1920x1080
- Video Codec: H.264 (libx264)
- Video Bitrate: 6 Mbps
- Audio Codec: AAC
- Audio Bitrate: 160 kbps
- Frame Rate: 60 FPS
- Preset: veryfast
- Format: FLV

---

## Custom FFmpeg Options

### Basic Custom Options

```json
{
  "input_name": "MainCamera",
  "output_name": "Custom",
  "input_url": "rtmp://source.example.com/live/stream",
  "output_url": "rtmp://destination.example.com/live/key",
  "ffmpeg_options": {
    "video_codec": "libx264",
    "preset": "medium",
    "bitrate": "5000k",
    "fps": "30"
  }
}
```

### Advanced Custom Options

```json
{
  "ffmpeg_options": {
    "video_codec": "libx264",
    "audio_codec": "aac",
    "preset": "slow",
    "crf": "18",
    "bitrate": "8000k",
    "maxrate": "8000k", 
    "bufsize": "16000k",
    "fps": "60",
    "resolution": "1920x1080",
    "audio_bitrate": "320k",
    "audio_sample_rate": "48000",
    "keyint": "60",
    "profile": "high",
    "level": "4.1",
    "pixel_format": "yuv420p",
    "threads": "4"
  }
}
```

### Video Filters and Transformations

```json
{
  "ffmpeg_options": {
    "video_filter": "scale=1280:720,fps=30",
    "video_codec": "libx264",
    "preset": "fast",
    "bitrate": "3000k"
  }
}
```

**Common Video Filters:**
- `scale=1280:720`: Resize to 720p
- `fps=30`: Set frame rate to 30 FPS
- `transpose=1`: Rotate 90 degrees clockwise
- `transpose=2`: Rotate 90 degrees counter-clockwise
- `crop=1080:1080:420:0`: Crop to square format
- `pad=1920:1080:0:0:black`: Add padding

### Audio Processing

```json
{
  "ffmpeg_options": {
    "audio_codec": "aac",
    "audio_bitrate": "128k",
    "audio_sample_rate": "44100",
    "audio_channels": "2",
    "audio_filter": "volume=0.8"
  }
}
```

---

## Copy Mode (No Transcoding)

For pure stream relay without transcoding:

```json
{
  "input_name": "MainCamera",
  "output_name": "Copy-Relay",
  "input_url": "rtmp://source.example.com/live/stream",
  "output_url": "rtmp://destination.example.com/live/key"
}
```

**Note:** When neither `platform_preset` nor `ffmpeg_options` are specified, Go-MLS automatically uses copy mode (`-c copy`), which provides:
- Zero transcoding overhead
- Minimal CPU usage
- Lowest latency
- Preserved original quality

---

## Input Source Types

### RTMP Sources
```json
{
  "name": "RTMPSource",
  "url": "rtmp://live.example.com/live/stream_key"
}
```

### RTSP Sources
```json
{
  "name": "RTSPCamera",
  "url": "rtsp://username:password@camera.local:554/stream1"
}
```

### HLS Sources
```json
{
  "name": "HLSStream",
  "url": "https://example.com/playlist.m3u8"
}
```

### File Sources
```json
{
  "name": "VideoFile",
  "url": "/path/to/video.mp4"
}
```

### UDP/RTP Sources
```json
{
  "name": "UDPStream",
  "url": "udp://224.1.1.1:5000"
}
```

---

## Output Destination Types

### RTMP Destinations
```json
{
  "output_url": "rtmp://live.platform.com/live/stream_key"
}
```

### RTSP Destinations
```json
{
  "output_url": "rtsp://server.local:554/stream_name"
}
```

### File Outputs
```json
{
  "output_url": "/recordings/output.mp4"
}
```

### HLS Outputs
```json
{
  "output_url": "/var/www/html/hls/stream.m3u8"
}
```

---

## Recording Configuration

### Basic Recording Setup

Recording is managed through the API, not configuration files:

```bash
# Start recording via API
curl -X POST http://localhost:8080/api/start-recording \
  -H "Content-Type: application/json" \
  -d '{
    "name": "MainCamera",
    "source": "rtmp://source.example.com/live/stream"
  }'
```

### Recording Directory Structure

```
recordings/
├── MainCamera_20250618_143000.mp4
├── SecondaryCamera_20250618_144500.mp4
└── EventStream_20250618_150000.mp4
```

### Recording Settings

Environment variables for recording configuration:

```bash
# Recording directory
export GO_MLS_RECORDINGS_DIR=/var/recordings

# Maximum file size (optional)
export GO_MLS_MAX_RECORDING_SIZE=5GB

# Recording format
export GO_MLS_RECORDING_FORMAT=mp4

# Recording quality (copy mode preserves original)
export GO_MLS_RECORDING_QUALITY=copy
```

---

## Monitoring Configuration

### Prometheus Integration

Add to `prometheus.yml`:

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'go-mls'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
    scrape_interval: 10s
```

### Grafana Dashboard

Environment variables for monitoring:

```bash
export GO_MLS_METRICS_ENABLED=true
export GO_MLS_METRICS_INTERVAL=10s
export GO_MLS_HEALTH_CHECK_INTERVAL=30s
```

---

## Security Configuration

### Basic Security Settings

```bash
# Disable debug endpoints in production
export GO_MLS_DEBUG_ENDPOINTS=false

# Enable CORS for specific origins
export GO_MLS_CORS_ORIGINS="https://admin.example.com,https://dashboard.example.com"

# Set request size limits
export GO_MLS_MAX_REQUEST_SIZE=10MB

# Configure timeouts
export GO_MLS_REQUEST_TIMEOUT=30s
export GO_MLS_SHUTDOWN_TIMEOUT=60s
```

### Reverse Proxy Configuration (nginx)

```nginx
server {
    listen 80;
    server_name go-mls.example.com;
    
    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    
    # Increase body size for config uploads
    client_max_body_size 10M;
    
    # Timeout settings
    proxy_connect_timeout 60s;
    proxy_send_timeout 60s;
    proxy_read_timeout 60s;
}
```

---

## Performance Tuning

### High-Volume Configuration

For handling many concurrent streams:

```bash
# Increase system limits
ulimit -n 65536

# Go runtime settings
export GOMAXPROCS=8
export GOGC=50

# FFmpeg process limits
export GO_MLS_MAX_CONCURRENT_STREAMS=100
export GO_MLS_FFMPEG_BUFFER_SIZE=8M
```

### Low-Latency Configuration

For minimal latency streaming:

```json
{
  "ffmpeg_options": {
    "preset": "ultrafast",
    "tune": "zerolatency",
    "buffer_size": "64k",
    "max_delay": "0",
    "fflags": "+nobuffer+flush_packets"
  }
}
```

### Resource-Constrained Configuration

For limited CPU/memory environments:

```json
{
  "ffmpeg_options": {
    "preset": "veryfast",
    "threads": "2",
    "bitrate": "2000k",
    "buffer_size": "4M"
  }
}
```

---

## Example Deployment Configurations

### Development Environment

```bash
# Start with debug logging and local access
./go-mls -port 8080 -host 127.0.0.1 -log-level debug
```

### Production Environment

```bash
# Production settings with external access
./go-mls \
  -port 80 \
  -host 0.0.0.0 \
  -log-level warn \
  -config /etc/go-mls/production.json
```

### Docker Compose Example

```yaml
version: '3.8'

services:
  go-mls:
    build: .
    ports:
      - "8080:8080"
      - "8554:8554"
    environment:
      - GO_MLS_PORT=8080
      - GO_MLS_HOST=0.0.0.0
      - GO_MLS_LOG_LEVEL=info
      - GO_MLS_RECORDINGS_DIR=/recordings
    volumes:
      - ./recordings:/recordings
      - ./config:/config
    restart: unless-stopped

  prometheus:
    image: prom/prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml

  grafana:
    image: grafana/grafana
    ports:
      - "3000:3000"
    volumes:
      - grafana-storage:/var/lib/grafana

volumes:
  grafana-storage:
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: go-mls
spec:
  replicas: 3
  selector:
    matchLabels:
      app: go-mls
  template:
    metadata:
      labels:
        app: go-mls
    spec:
      containers:
      - name: go-mls
        image: go-mls:latest
        ports:
        - containerPort: 8080
        - containerPort: 8554
        env:
        - name: GO_MLS_PORT
          value: "8080"
        - name: GO_MLS_HOST
          value: "0.0.0.0"
        - name: GO_MLS_LOG_LEVEL
          value: "info"
        volumeMounts:
        - name: recordings
          mountPath: /recordings
        - name: config
          mountPath: /config
      volumes:
      - name: recordings
        persistentVolumeClaim:
          claimName: go-mls-recordings
      - name: config
        configMap:
          name: go-mls-config
```

This configuration guide provides comprehensive examples for various deployment scenarios and use cases.
