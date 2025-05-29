# Go-MLS: Unified Go Service for Streaming and Control

This project implements a single Go service for video streaming/relay, web UI control, process management, and static asset serving. All logic is in Go, with static HTML/JS/CSS for the frontend. No PHP, Nginx, or bash scripts required.

## Features
- Video streaming/relay (RTMP/WebSocket integration planned)
- Web UI for control and configuration
- Process management for streams
- Static asset serving

## Getting Started
1. Install Go 1.18+.
2. Build and run the service:
   ```bash
   go run main.go
   ```
3. Access the web UI at http://localhost:8080

## Project Structure
- `main.go` - Entry point for the Go service
- `internal/` - Go packages for streaming, config, and process management
- `web/static/` - Static assets (HTML, JS, CSS)

## Roadmap
- [ ] Implement RTMP/WebSocket relay in Go
- [ ] Build REST API for control/config
- [ ] Migrate UI to static assets
- [ ] Replace all shell/PHP logic with Go

---
This README will be updated as the project evolves.
