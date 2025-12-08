// Package stream provides core streaming functionality for the go-mls application.
//
// Core Managers:
//   - StreamManager: Orchestrates input/output relays and manages consumers. It is the central hub for stream management.
//   - InputRelayManager: Decodes input streams (RTMP, RTSP, etc.) and serves them via an embedded RTSP server.
//   - OutputRelayManager: Relays RTSP streams to external destinations (RTMP/RTSP).
//   - RecordingManager: Records streams to MP4 files using FFmpeg.
//   - HLSManager: Converts streams to HLS format for web browsers using FFmpeg.
//
// Key Interfaces:
//   - StreamProvider: Allows consumers (Recording, HLS) to request streams without knowing about the underlying input implementation.
//   - Consumer: A unified interface for managing relays, recordings, and HLS sessions.
//   - ConsumerCleanupHandler: Handles cleanup callbacks when consumers fail or stop.
//
// Usage Pattern:
//  1. Create a StreamManager with input/output/RTSP managers.
//  2. Call StartStream() to begin relaying an input.
//  3. Consumers (Recording, HLS) use the StreamProvider interface to access the stream.
//  4. On failure, a Consumer calls OnFailure() to trigger cleanup.
package stream
