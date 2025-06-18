# Go-MLS Documentation Index

This directory contains comprehensive documentation for Go-MLS, a powerful live streaming solution built in Go.

## 📚 Documentation Structure

### Core Documentation
- **[README.md](../README.md)** - Project overview and quick start guide
- **[Architecture Guide](./architecture.md)** - Detailed system architecture and component overview
- **[API Reference](./api.md)** - Complete REST API documentation with examples

### Setup and Configuration
- **[Configuration Guide](./configuration.md)** - Configuration options, platform presets, and examples
- **[Deployment Guide](./deployment.md)** - Production deployment strategies and infrastructure
- **[Call Flows](./call-flows.md)** - Detailed operation flows with sequence diagrams

### Operations and Maintenance  
- **[Monitoring Guide](./monitoring.md)** - Observability, metrics, and alerting setup
- **[Troubleshooting Guide](./troubleshooting.md)** - Common issues and debugging techniques

## 🚀 Quick Navigation

### For Developers
Start with the [Architecture Guide](./architecture.md) to understand the system design, then check the [API Reference](./api.md) for integration details.

### For DevOps/SRE
Begin with the [Deployment Guide](./deployment.md) for infrastructure setup, then configure [Monitoring](./monitoring.md) for observability.

### For System Administrators
Start with [Configuration Guide](./configuration.md) for setup options, keep [Troubleshooting Guide](./troubleshooting.md) handy for operations.

### For Users
Check the main [README.md](../README.md) for basic usage, then explore [Configuration Guide](./configuration.md) for advanced features.

## 📖 Documentation Features

### Interactive Examples
All documentation includes:
- **Working code examples** that you can copy and run
- **curl commands** for API testing
- **Configuration samples** for different use cases
- **Docker/Kubernetes manifests** for container deployments

### Visual Diagrams
- **Architecture diagrams** showing component relationships
- **Sequence diagrams** for operation flows  
- **Network diagrams** for deployment topologies
- **Monitoring dashboards** for observability

### Production-Ready Guides
- **Security best practices** for hardening deployments
- **Performance tuning** recommendations
- **Scaling strategies** for high-volume scenarios
- **Disaster recovery** procedures

## 🔧 Platform-Specific Guides

The documentation covers configuration and optimization for major streaming platforms:

| Platform | Preset | Features |
|----------|--------|----------|
| YouTube | `youtube` | 1080p, 6Mbps, optimized for quality |
| Instagram | `instagram` | 1080x1920, vertical orientation |
| TikTok | `tiktok` | 1080x1920, mobile-optimized |
| Facebook | `facebook` | 720p, social media optimized |
| Twitch | `twitch` | 1080p, gaming optimized |

## 🛠️ Use Case Examples

### Basic Streaming Setup
```bash
# Single input to multiple outputs
curl -X POST http://localhost:8080/api/start-relay \
  -H "Content-Type: application/json" \
  -d '{
    "input_url": "rtmp://source.com/live/stream",
    "output_url": "rtmp://a.rtmp.youtube.com/live2/KEY",
    "input_name": "MainCamera",
    "output_name": "YouTube",
    "platform_preset": "youtube"
  }'
```

### Advanced Configuration
```json
{
  "endpoints": [{
    "input_name": "Camera1",
    "output_name": "Custom",
    "input_url": "rtmp://input.com/live/stream",
    "output_url": "rtmp://output.com/live/key",
    "ffmpeg_options": {
      "video_codec": "libx264",
      "preset": "medium",
      "bitrate": "5000k",
      "resolution": "1280x720"
    }
  }]
}
```

### Monitoring Setup
```yaml
# Prometheus configuration
scrape_configs:
  - job_name: 'go-mls'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
```

## 📋 Checklists

### Pre-Deployment Checklist
- [ ] ffmpeg installed and in PATH
- [ ] Network ports open (8080, 8554)
- [ ] Sufficient system resources allocated
- [ ] Configuration file validated
- [ ] Monitoring setup configured

### Production Readiness Checklist
- [ ] TLS/SSL certificates configured
- [ ] Backup and recovery procedures tested
- [ ] Monitoring and alerting active
- [ ] Log rotation configured
- [ ] Resource limits set
- [ ] Security hardening applied

### Performance Optimization Checklist
- [ ] Hardware acceleration enabled (if available)
- [ ] Network buffers tuned for streaming
- [ ] ffmpeg presets optimized for use case
- [ ] System resource monitoring active
- [ ] Scaling thresholds defined

## 🤝 Contributing to Documentation

We welcome contributions to improve the documentation:

1. **Content Updates**: Fix errors, add missing information, improve clarity
2. **Examples**: Add real-world use cases and configuration examples  
3. **Translations**: Help translate documentation to other languages
4. **Diagrams**: Improve or add visual diagrams and flowcharts

### Documentation Standards
- Use clear, concise language
- Include working examples
- Follow the established structure
- Test all code examples
- Update the index when adding new sections

## 🆘 Getting Help

If you can't find what you're looking for:

1. **Search the documentation** using your browser's search function
2. **Check the troubleshooting guide** for common issues
3. **Review the API reference** for integration questions
4. **Open an issue** on GitHub for documentation improvements
5. **Join the community** discussions for support

## 📝 Documentation Versions

This documentation is versioned with the Go-MLS releases:

- **Latest**: Current development version
- **Stable**: Latest stable release
- **Archive**: Previous versions for reference

Always use the documentation version that matches your Go-MLS installation.

---

*Last updated: June 18, 2025*  
*Documentation version: 1.0.0*  
*Go-MLS version compatibility: 1.0.0+*
