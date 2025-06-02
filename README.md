# Go-MLS: Go Media Live Streamer

Go-MLS is a single Go service for video streaming and relay, web UI control, process management, and static asset serving. All backend logic is implemented in Go, and the frontend uses static HTML, JavaScript, and CSS—no PHP, Nginx, or bash scripts required.

## Features
- RTMP relay and video streaming using ffmpeg
- Web-based UI for controlling streams and relays
- Process management for multiple concurrent ffmpeg relays
- Real-time log streaming and status monitoring
- Serves static frontend assets (HTML/JS/CSS) directly from Go

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

### Usage
- Access the web UI at `http://localhost:8080` (or the configured port)
- Configure input and output RTMP URLs via the web interface
- Start, stop, and update relays in real time
- View relay status
