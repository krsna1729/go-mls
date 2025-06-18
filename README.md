# Go-MLS: Go Media Live Streamer

Go-MLS is a powerful, scalable live streaming solution built in Go that enables you to ingest multiple media sources and relay them to hundreds of destinations simultaneously. It provides a complete streaming infrastructure with web-based management, recording capabilities, and advanced stream processing features.

## 🎯 Key Features

### Core Streaming Capabilities
- **Multi-source ingestion**: RTMP, RTSP, HLS, and file-based inputs
- **Multi-destination relay**: Stream to hundreds of destinations simultaneously
- **Dynamic management**: Add, remove, and modify streams without affecting others
- **Platform presets**: Instagram, TikTok, YouTube, Facebook optimized settings
- **Custom ffmpeg options**: Full control over encoding parameters
- **Copy mode**: Pure stream passthrough without re-encoding

### Advanced Stream Processing
- **Multiple resolutions**: Send different resolutions per destination
- **Video rotation**: Vertical rotation for mobile platforms (Instagram, TikTok)
- **Audio track selection**: Route different audio tracks per destination
- **Codec flexibility**: Support for different video/audio codecs per output
- **Bitrate control**: Configure different bitrates per destination
- **Frame rate adjustment**: Customize frame rates per output
- **Aspect ratio conversion**: Dynamic aspect ratio changes

### Recording & Monitoring
- **Stream recording**: Record incoming streams to disk
- **Browser downloads**: Download recordings via web interface
- **File streaming**: Stream recorded files to destinations
- **Real-time monitoring**: Live statistics and performance metrics
- **Search capabilities**: Find sources and destinations by name or URL
- **Bitrate reporting**: Real-time bitrate monitoring per output

### Management & Control
- **Web-based UI**: Complete control via modern web interface
- **RESTful API**: Programmatic control and integration
- **Configuration management**: Import/export configurations
- **Process monitoring**: Track ffmpeg processes and system resources
- **Grafana integration**: Advanced monitoring with Prometheus metrics

## 🏗️ Architecture

Go-MLS uses a modular architecture built on top of MediaMTX as the RTSP backend:

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  Input Sources  │───▶│   Go-MLS Core   │───▶│  Output Relays  │
└─────────────────┘    └─────────────────┘    └─────────────────┘
│                      │                      │                 │
├─ RTMP Streams        ├─ Relay Manager       ├─ RTMP Outputs   │
├─ RTSP Streams        ├─ Recording Manager   ├─ RTSP Outputs   │
├─ HLS Streams         ├─ RTSP Server         ├─ HLS Outputs    │
└─ File Sources        ├─ Web UI Server       └─ File Outputs   │
                       └─ Monitoring API                        │
```

## 🚀 Quick Start

### Prerequisites
- **Go 1.23+**: Required for building the application
- **ffmpeg**: Must be installed and available in PATH
- **MediaMTX**: Embedded RTSP server (included)
- **Docker** (optional): For containerized deployment

### Installation

#### Option 1: Direct Build
```bash
git clone https://github.com/krsna/go-mls.git
cd go-mls
go build -o go-mls
./go-mls
```

#### Option 2: Docker Compose
```bash
git clone https://github.com/krsna/go-mls.git
cd go-mls
docker-compose up -d
```

### Basic Usage

1. **Access the Web UI**: Open `http://localhost:8080`
2. **Add Input Source**: Configure your RTMP/RTSP input stream
3. **Add Output Destinations**: Set up your streaming destinations
4. **Start Relaying**: Begin streaming to all configured outputs
5. **Monitor**: View real-time statistics and logs

### Configuration

The application can be configured via:
- Command-line flags
- Environment variables
- Configuration files (`relay_config.json`)

```bash
./go-mls -port 8080 -host 0.0.0.0 -config relay_config.json
```

## 📚 Documentation

For detailed documentation, see the [`docs/`](./docs/) directory:

- **[Architecture Guide](./docs/architecture.md)**: Detailed system architecture
- **[API Reference](./docs/api.md)**: Complete REST API documentation
- **[Call Flows](./docs/call-flows.md)**: Detailed operation flows with diagrams
- **[Configuration Guide](./docs/configuration.md)**: Configuration options and examples
- **[Deployment Guide](./docs/deployment.md)**: Production deployment strategies
- **[Monitoring Guide](./docs/monitoring.md)**: Observability and metrics
- **[Troubleshooting](./docs/troubleshooting.md)**: Common issues and solutions

## 🔧 Platform Presets

Go-MLS includes optimized presets for popular streaming platforms:

| Platform | Resolution | Bitrate | FPS | Codec | Special Features |
|----------|------------|---------|-----|-------|------------------|
| YouTube | 1920x1080 | 6000k | 30 | H.264 | High quality |
| Instagram | 1080x1920 | 3500k | 30 | H.264 | Vertical orientation |
| TikTok | 1080x1920 | 2500k | 30 | H.264 | Vertical, optimized |
| Facebook | 1280x720 | 4000k | 30 | H.264 | Social optimized |
| Twitch | 1920x1080 | 6000k | 60 | H.264 | Gaming optimized |

## 🎛️ API Examples

### Start a Relay with Platform Preset
```bash
curl -X POST http://localhost:8080/api/start-relay \
  -H "Content-Type: application/json" \
  -d '{
    "input_url": "rtmp://source.example.com/live/stream",
    "output_url": "rtmp://a.rtmp.youtube.com/live2/YOUR_KEY",
    "input_name": "MainCamera",
    "output_name": "YouTube",
    "platform_preset": "youtube"
  }'
```

### Start Recording
```bash
curl -X POST http://localhost:8080/api/start-recording \
  -H "Content-Type: application/json" \
  -d '{
    "name": "MainCamera",
    "source": "rtmp://source.example.com/live/stream"
  }'
```

### Get Status
```bash
curl http://localhost:8080/api/status
```

## 🔍 Monitoring

Go-MLS provides comprehensive monitoring:

- **Real-time metrics**: Bitrate, frame rate, resolution, errors
- **Prometheus integration**: Export metrics for Grafana dashboards
- **Health checks**: Monitor stream health and ffmpeg processes
- **Log aggregation**: Centralized logging with severity levels

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](./CONTRIBUTING.md) for details.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.

## 🆘 Support

- **Documentation**: Check the [docs/](./docs/) directory
- **Issues**: Report bugs on [GitHub Issues](https://github.com/krsna/go-mls/issues)
- **Discussions**: Join [GitHub Discussions](https://github.com/krsna/go-mls/discussions)
