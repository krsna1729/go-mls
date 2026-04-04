# REFACTOR.md

> **DEPRECATED - Historical Document**
>
> This document describes the original Go-MLS architecture (v1.0).
>
> **The current architecture (v2.0) is now the default.** See:
> - [Current Architecture](docs/architecture.md)
> - [API Reference](docs/api-reference.md)
> - [Configuration](docs/configuration.md)

---

## Summary of Changes (v1.0 → v2.0)

### Deleted Legacy Code
- `internal/stream/` - Legacy pipeline-based architecture
- Old API router (`internal/api/router.go`)

### New Architecture Components
- `internal/hub/` - Unified hub interface supporting RTMP and RTSP
- `internal/worker/` - Worker-based FFmpeg process management
- `internal/ingest/` - Smart ingest router
- `internal/state/` - Centralized state management
- `internal/api/server.go` - Unified API server

### Key Features
1. **Configurable Hub Type** - Switch between RTMP and RTSP via `relay.hub_type` config
2. **Worker Lifecycle Pattern** - Proper Start/Stop/Wait with goroutine management
3. **State-Based Design** - Centralized state with export/import support
4. **Mutex Protection** - Thread-safe access to shared resources
