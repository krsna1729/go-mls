// Package ingest implements the Smart Ingest Router.
// It supports Native Pulling (RTMP/S), FFmpeg Pulling (RTSP/SRT/HLS),
// and Passive Accepting (OBS push) with token-based gatekeeping.
package ingest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/state"
	"go-mls/internal/worker"
)

// Router manages ingestion of streams into the system.
type Router struct {
	store    *state.Store
	log      *logger.Logger
	rtmpPort int
	rtspPort int
	srtHost  string
	srtPort  int
	mu       sync.RWMutex

	evictStream func(streamPath string)

	// Map of stream_path -> active Puller for pull-mode inputs
	pullers map[string]*worker.Puller

	// Map of stream_path -> active SRT-to-RTMP adapter for srt accept-mode inputs.
	srtAdapters map[string]*worker.SRTAdapter

	// Map of stream_path -> active RTSP-to-RTMP adapter for rtsp accept-mode inputs.
	rtspAdapters map[string]*worker.RTSPAdapter
}

// Config holds router configuration.
type Config struct {
	RTMPPort int
	RTSPPort int
	SRTHost  string
	SRTPort  int
}

// NewRouter creates a new ingest router.
func NewRouter(store *state.Store, log *logger.Logger, cfg Config) *Router {
	srtHost := cfg.SRTHost
	if strings.TrimSpace(srtHost) == "" {
		srtHost = "0.0.0.0"
	}
	srtPort := cfg.SRTPort
	if srtPort <= 0 {
		srtPort = 9000
	}

	return &Router{
		store:        store,
		log:          log.With("component", "ingest"),
		rtmpPort:     cfg.RTMPPort,
		rtspPort:     cfg.RTSPPort,
		srtHost:      srtHost,
		srtPort:      srtPort,
		pullers:      make(map[string]*worker.Puller),
		srtAdapters:  make(map[string]*worker.SRTAdapter),
		rtspAdapters: make(map[string]*worker.RTSPAdapter),
	}
}

// SetStreamEvictor registers a callback that forcefully evicts active publishers
// for a stream path at the hub layer.
func (r *Router) SetStreamEvictor(evictor func(streamPath string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictStream = evictor
}

// RegisterInput registers an input and starts ingestion if it's a puller.
// For acceptors (no remote_url), it just reserves the path and waits for a push.
func (r *Router) RegisterInput(ctx context.Context, in *state.Input) error {
	// Determine mode
	if in.RemoteURL == "" {
		if in.AcceptProtocol == "" {
			in.AcceptProtocol = "rtmp"
		}
		in.AcceptProtocol = normalizeAcceptProtocol(in.AcceptProtocol)
		in.Mode = state.InputModeAccept
		in.Status = state.InputStatusStarting
	} else {
		in.AcceptProtocol = ""
		in.Mode = state.InputModePull
		in.Status = state.InputStatusStarting
	}

	if err := r.store.AddInput(in); err != nil {
		return err
	}

	if in.Mode == state.InputModePull {
		go r.startPuller(ctx, in)
	} else if in.AcceptProtocol == "srt" {
		r.log.Info("SRT adapter input registered, waiting for SRT publisher", "stream_path", in.StreamPath)
	} else if in.AcceptProtocol == "rtsp" {
		r.log.Info("RTSP adapter input registered, waiting for RTSP publisher", "stream_path", in.StreamPath)
	} else {
		r.log.Info("Acceptor registered, waiting for push", "stream_path", in.StreamPath)
	}

	return nil
}

// EnsureInputActive restarts a pull-mode input if it is registered but not currently pulling.
func (r *Router) EnsureInputActive(ctx context.Context, streamPath string) error {
	in, ok := r.store.GetInput(streamPath)
	if !ok {
		return fmt.Errorf("input %q not found", streamPath)
	}
	if in.Mode != state.InputModePull {
		return nil
	}
	if in.Status == state.InputStatusActive {
		return nil
	}
	if in.Status == state.InputStatusStarting {
		// A puller startup is already in progress; avoid spawning duplicates.
		return nil
	}

	r.mu.RLock()
	_, running := r.pullers[streamPath]
	r.mu.RUnlock()
	if running {
		return nil
	}

	r.store.UpdateInputStatus(streamPath, state.InputStatusStarting, "")
	go r.startPuller(ctx, in)
	return nil
}

// UnregisterInput stops any active puller and removes the input.
func (r *Router) UnregisterInput(streamPath string) error {
	r.mu.Lock()
	puller := r.pullers[streamPath]
	if puller != nil {
		delete(r.pullers, streamPath)
	}
	srtAdapter := r.srtAdapters[streamPath]
	if srtAdapter != nil {
		delete(r.srtAdapters, streamPath)
	}
	rtspAdapter := r.rtspAdapters[streamPath]
	if rtspAdapter != nil {
		delete(r.rtspAdapters, streamPath)
	}
	evictor := r.evictStream
	r.mu.Unlock()

	if puller != nil {
		puller.Stop()
	}
	if srtAdapter != nil {
		srtAdapter.Stop()
	}
	if rtspAdapter != nil {
		rtspAdapter.Stop()
	}

	if evictor != nil {
		evictor(streamPath)
	}

	if puller != nil {
		select {
		case <-puller.Done():
		case <-time.After(5 * time.Second):
			r.log.Warn("Timed out waiting for puller to stop during unregister", "stream_path", streamPath)
		}
	}

	if srtAdapter != nil {
		select {
		case <-srtAdapter.Done():
		case <-time.After(5 * time.Second):
			r.log.Warn("Timed out waiting for SRT adapter to stop during unregister", "stream_path", streamPath)
		}
	}

	if rtspAdapter != nil {
		select {
		case <-rtspAdapter.Done():
		case <-time.After(5 * time.Second):
			r.log.Warn("Timed out waiting for RTSP adapter to stop during unregister", "stream_path", streamPath)
		}
	}

	return r.store.RemoveInput(streamPath)
}

// Shutdown stops all active pullers and waits for them to complete.
func (r *Router) Shutdown() {
	r.mu.Lock()
	pullers := r.pullers
	r.pullers = make(map[string]*worker.Puller)
	srtAdapters := r.srtAdapters
	r.srtAdapters = make(map[string]*worker.SRTAdapter)
	rtspAdapters := r.rtspAdapters
	r.rtspAdapters = make(map[string]*worker.RTSPAdapter)
	r.mu.Unlock()

	for path, puller := range pullers {
		r.log.Info("Stopping puller", "stream_path", path)
		puller.Stop()
	}

	for path, adapter := range srtAdapters {
		r.log.Info("Stopping SRT adapter", "stream_path", path)
		adapter.Stop()
	}

	for path, adapter := range rtspAdapters {
		r.log.Info("Stopping RTSP adapter", "stream_path", path)
		adapter.Stop()
	}

	// Wait for all pullers to finish
	for path, puller := range pullers {
		select {
		case <-puller.Done():
		case <-time.After(10 * time.Second):
			r.log.Warn("Puller did not stop within timeout", "stream_path", path)
		}
	}

	for path, adapter := range srtAdapters {
		select {
		case <-adapter.Done():
		case <-time.After(10 * time.Second):
			r.log.Warn("SRT adapter did not stop within timeout", "stream_path", path)
		}
	}

	for path, adapter := range rtspAdapters {
		select {
		case <-adapter.Done():
		case <-time.After(10 * time.Second):
			r.log.Warn("RTSP adapter did not stop within timeout", "stream_path", path)
		}
	}
}

// startPuller starts the appropriate puller based on the remote URL protocol.
func (r *Router) startPuller(ctx context.Context, in *state.Input) {
	url := in.RemoteURL

	if isNativeRTMP(url) {
		r.log.Info("Starting native RTMP pull", "stream_path", in.StreamPath, "url", url)
		// For now, use FFmpeg for all pulls. Native RTMP pull can be implemented
		// later using gortmplib directly.
		r.startFFmpegPuller(ctx, in)
	} else {
		r.log.Info("Starting FFmpeg pull", "stream_path", in.StreamPath, "url", url)
		r.startFFmpegPuller(ctx, in)
	}
}

// startFFmpegPuller starts a worker.Puller for the given input.
func (r *Router) startFFmpegPuller(ctx context.Context, in *state.Input) {
	puller, err := worker.StartPuller(ctx, r.store, r.log, in, r.rtmpPort)
	if err != nil {
		r.log.Error("Failed to start puller", "stream_path", in.StreamPath, "error", err)
		return
	}

	r.mu.Lock()
	r.pullers[in.StreamPath] = puller
	r.mu.Unlock()

	// Monitor for exit and clean up
	go func(p *worker.Puller) {
		<-p.Done()
		r.mu.Lock()
		if current, ok := r.pullers[in.StreamPath]; ok && current == p {
			delete(r.pullers, in.StreamPath)
		}
		r.mu.Unlock()
	}(puller)
}

func (r *Router) startSRTAdapter(ctx context.Context, in *state.Input) {
	r.mu.RLock()
	_, running := r.srtAdapters[in.StreamPath]
	r.mu.RUnlock()
	if running {
		return
	}

	adapter, err := worker.StartSRTAdapter(ctx, r.store, r.log, in, r.srtPort, r.rtmpPort)
	if err != nil {
		r.log.Error("Failed to start SRT adapter", "stream_path", in.StreamPath, "error", err)
		r.store.UpdateInputStatus(in.StreamPath, state.InputStatusError, err.Error())
		return
	}

	r.mu.Lock()
	r.srtAdapters[in.StreamPath] = adapter
	r.mu.Unlock()

	r.store.UpdateInputRemoteAddr(in.StreamPath, fmt.Sprintf("srt://%s:%d?mode=caller&streamid=publish:%s", r.srtHost, r.srtPort, in.StreamPath))
	r.store.UpdateInputStatus(in.StreamPath, state.InputStatusActive, "")

	go func(a *worker.SRTAdapter, streamPath string) {
		<-a.Done()
		r.mu.Lock()
		if current, ok := r.srtAdapters[streamPath]; ok && current == a {
			delete(r.srtAdapters, streamPath)
		}
		r.mu.Unlock()
	}(adapter, in.StreamPath)
}

func (r *Router) startRTSPAdapter(ctx context.Context, in *state.Input) {
	r.mu.RLock()
	_, running := r.rtspAdapters[in.StreamPath]
	r.mu.RUnlock()
	if running {
		return
	}

	adapter, err := worker.StartRTSPAdapter(ctx, r.store, r.log, in, r.rtspPort, r.rtmpPort)
	if err != nil {
		r.log.Error("Failed to start RTSP adapter", "stream_path", in.StreamPath, "error", err)
		r.store.UpdateInputStatus(in.StreamPath, state.InputStatusError, err.Error())
		return
	}

	r.mu.Lock()
	r.rtspAdapters[in.StreamPath] = adapter
	r.mu.Unlock()

	r.store.UpdateInputRemoteAddr(in.StreamPath, fmt.Sprintf("rtsp://%s:%d/%s", "127.0.0.1", r.rtspPort, in.StreamPath))
	r.store.UpdateInputStatus(in.StreamPath, state.InputStatusActive, "")

	go func(a *worker.RTSPAdapter, streamPath string) {
		<-a.Done()
		r.mu.Lock()
		if current, ok := r.rtspAdapters[streamPath]; ok && current == a {
			delete(r.rtspAdapters, streamPath)
		}
		r.mu.Unlock()
	}(adapter, in.StreamPath)
}

func normalizeAcceptProtocol(protocol string) string {
	p := strings.ToLower(strings.TrimSpace(protocol))
	if p == "" {
		return "rtmp"
	}
	if p == "srt" {
		return "srt"
	}
	if p == "rtsp" {
		return "rtsp"
	}
	return "rtmp"
}

// ValidateToken checks if the provided token matches the registered ingest token.
func (r *Router) ValidateToken(streamPath, token string) bool {
	in, ok := r.store.GetInput(streamPath)
	if !ok {
		return false // Path not registered
	}
	if in.IngestToken == "" {
		return true // No token required
	}
	return in.IngestToken == token
}

// OnPublish is called by ingest hubs when a publisher connects.
// It validates the path and token, then activates or adapts the input.
func (r *Router) OnPublish(streamPath, token, remoteAddr string) error {
	in, ok := r.store.GetInput(streamPath)
	if !ok {
		return fmt.Errorf("stream path %q not registered", streamPath)
	}
	if in.Mode == state.InputModeAccept && normalizeAcceptProtocol(in.AcceptProtocol) == "rtsp" {
		r.mu.RLock()
		_, running := r.rtspAdapters[streamPath]
		r.mu.RUnlock()
		if running || strings.HasPrefix(remoteAddr, "127.0.0.1:") || strings.HasPrefix(remoteAddr, "[::1]:") {
			r.store.UpdateInputStatus(streamPath, state.InputStatusActive, "")
			return nil
		}
		if remoteAddr != "" {
			r.store.UpdateInputRemoteAddr(streamPath, remoteAddr)
		}
		r.store.UpdateInputStatus(streamPath, state.InputStatusStarting, "")
		go r.startRTSPAdapter(context.Background(), in)
		r.log.Info("RTSP publisher connected, adapter starting", "stream_path", streamPath, "remote_addr", remoteAddr)
		return nil
	}
	if in.Mode == state.InputModeAccept && normalizeAcceptProtocol(in.AcceptProtocol) == "srt" {
		r.mu.RLock()
		_, running := r.srtAdapters[streamPath]
		r.mu.RUnlock()
		if running {
			r.store.UpdateInputStatus(streamPath, state.InputStatusActive, "")
			return nil
		}
		if remoteAddr != "" {
			r.store.UpdateInputRemoteAddr(streamPath, remoteAddr)
		}
		r.store.UpdateInputStatus(streamPath, state.InputStatusStarting, "")
		go r.startSRTAdapter(context.Background(), in)
		r.log.Info("SRT publisher connected, adapter starting", "stream_path", streamPath, "remote_addr", remoteAddr)
		return nil
	}
	if in.IngestToken != "" && in.IngestToken != token {
		return fmt.Errorf("invalid ingest token for %q", streamPath)
	}
	if remoteAddr != "" {
		r.store.UpdateInputRemoteAddr(streamPath, remoteAddr)
	}
	r.store.UpdateInputStatus(streamPath, state.InputStatusActive, "")
	r.log.Info("Publisher connected", "stream_path", streamPath, "remote_addr", remoteAddr)
	return nil
}

// OnPublishEnd is called when a publisher disconnects.
func (r *Router) OnPublishEnd(streamPath string) {
	r.mu.Lock()
	if adapter, ok := r.srtAdapters[streamPath]; ok {
		delete(r.srtAdapters, streamPath)
		r.mu.Unlock()
		adapter.Stop()
	} else {
		r.mu.Unlock()
	}
	r.store.UpdateInputStatus(streamPath, state.InputStatusStopped, "")
	r.log.Info("Publisher disconnected", "stream_path", streamPath)
}

func isNativeRTMP(url string) bool {
	return strings.HasPrefix(url, "rtmp://") || strings.HasPrefix(url, "rtmps://")
}
