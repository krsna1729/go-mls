> **⚠️ Historical Document**
> 
> This document describes the original Go-MLS architecture (v1.0). The current architecture (v2.0) uses a simplified Pipeline pattern.
> 
> For current documentation, see:
> - [Architecture Overview](docs/architecture.md)
> - [API Reference](docs/api-reference.md)
> - [Configuration](docs/configuration.md)

---

This is the definitive Master Technical Specification for the Go-Media-Engine. It consolidates every architectural decision, security protocol, and persistence mechanism we have designed into a single, cohesive blueprint.
📑 Master Technical Specification: Go-Media-Engine (v1.2)
1. Executive Summary
Go-Media-Engine is a high-performance, secure media gateway designed for high-density stream management. It utilizes Go for non-blocking RTMP memory routing and FFmpeg for modular, ephemeral tasks (transcoding, recording, HLS generation). The system features an "Ingest Gatekeeper" for security and a "State Persistence Layer" for disaster recovery.
2. Core Architecture Components
A. The Hub (Go + gortmplib)
 * RTMP Ingest (Port 1935): Handles handshakes and validates incoming tokens.
 * Memory Router: Implements a 1-to-N fan-out using zero-copy Go slices to distribute video data to local and remote listeners.
 * HTTP Control Plane (Port 8080): Manages JSON APIs, serves HLS files, and handles the persistence lifecycle.
B. The Smart Ingest Router
 * Native Puller (RTMP/S): Directly pulls from remote servers into Go memory.
 * FFmpeg Puller (RTSP/SRT/HLS): Spawns a monitored FFmpeg process to translate protocols into a local RTMP feed.
 * Passive Acceptor: Listens for incoming pushes (e.g., OBS) on reserved paths.
C. The Worker Layer (FFmpeg)
 * HLS Generator: Lazy-loaded; only active when viewers are present.
 * Recorder: Dynamic; captures -c copy MP4 files.
 * Restreamer: Dynamic; handles multi-destination pushes with per-output transcoding parameters.
3. Security & Access Control
A. Acceptor Gatekeeping
 * Publishers MUST provide an ingest_token as a query parameter (e.g., rtmp://server/path?token=XYZ).
 * The Go server rejects any connection where the path is not pre-registered via API or the token does not match the reserved value.
B. Credential Management
 * All sensitive credentials (tokens, remote stream keys) are transmitted via HTTP POST JSON Body to ensure TLS encryption. They are never exposed in URL query strings of the API.
4. Monitoring & Telemetry Spec
Implement a runAndMonitorFFmpeg wrapper for all workers:
 * Hardware: Poll PID every 2s using gopsutil for CPU % and Memory RSS (MB).
 * Video: Parse FFmpeg stderr progress lines for frame=, fps=, bitrate=, and speed=.
 * Parsing: Use a custom scanner to handle \r (carriage returns) to extract real-time updates.
5. Persistence & Auto-Resume Spec
A. Auto-Save Mechanism
 * Any API-driven change (Adding/Removing inputs or outputs) triggers an Atomic Write to config.json.
 * Atomic Write Flow: Write to config.json.tmp -> os.Rename to config.json.
 * Stored State: Includes stream_path, remote_url, ingest_token, and all nested outputs with their specific transcode_args.
B. The Resume Workflow (--resume flag)
 * Bootstrap: If the --resume flag is passed at startup, Go loads config.json.
 * Phase 1 (Inputs): Immediately trigger SmartPullRouter for pullers or reserve paths for acceptors.
 * Phase 2 (Outputs - Async): For each output, spawn a "Waiting Worker":
   * Poll the internal stream map every 2s.
   * Once the input stream becomes "Active" (handshake complete), spawn the FFmpeg restreamer.
   * Timeout after 5 minutes of inactivity.
6. HTTP API Reference
| Method | Endpoint | Body (JSON) / Query | Function |
|---|---|---|---|
| POST | /inputs | {"stream_path": "...", "remote_url": "...", "ingest_token": "..."} | Register an Acceptor or start a Puller. |
| POST | /outputs | {"stream_path": "...", "output_id": "...", "remote_url": "...", "stream_key": "...", "video_args": [], "audio_args": []} | Start a monitored remote push. |
| DELETE | /outputs | ?stream=path&id=id | Stop a specific remote push. |
| POST | /record | ?stream=path | Start MP4 recording. |
| DELETE | /record | ?stream=path | Finalize MP4 recording. |
| GET | /stats | N/A | Return nested JSON of hardware and video telemetry. |
| GET | /system/export | N/A | Manual download of current config.json. |
| POST | /system/import | Snapshot JSON | Force manual re-hydration from a provided JSON. |
7. Implementation Lifecycle
 * Setup: Initialize Go modules and install ffmpeg.
 * Develop: Build the runAndMonitorFFmpeg utility first.
 * Assemble: Create the ServerHandler for RTMP and the mux for HTTP.
 * Integrate: Implement the persistence.go logic for auto-saving.
 * Containerize: Use the multi-stage Dockerfile (Go builder + Alpine/FFmpeg runner).
Verification Test Plan
 * Ad-hoc Security: Attempt OBS push without a token. (Expected: Reject).
 * Lazy HLS: Request .m3u8. (Expected: FFmpeg starts). Wait 15s. (Expected: FFmpeg dies).
 * Telemetry: Start a transcode. (Expected: /stats shows high CPU for that output PID).
 * Persistence: Add a stream, kill the server, restart with --resume. (Expected: Stream auto-reconnects).
