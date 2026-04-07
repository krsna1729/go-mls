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
	mu       sync.RWMutex

	evictStream func(streamPath string)

	// Map of stream_path -> active Puller for pull-mode inputs
	pullers map[string]*worker.Puller
}

// Config holds router configuration.
type Config struct {
	RTMPPort int
}

// NewRouter creates a new ingest router.
func NewRouter(store *state.Store, log *logger.Logger, cfg Config) *Router {
	return &Router{
		store:    store,
		log:      log.With("component", "ingest"),
		rtmpPort: cfg.RTMPPort,
		pullers:  make(map[string]*worker.Puller),
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
		in.Mode = state.InputModeAccept
		in.Status = state.InputStatusStarting
	} else {
		in.Mode = state.InputModePull
		in.Status = state.InputStatusStarting
	}

	if err := r.store.AddInput(in); err != nil {
		return err
	}

	if in.Mode == state.InputModePull {
		go r.startPuller(ctx, in)
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
	evictor := r.evictStream
	r.mu.Unlock()

	if puller != nil {
		puller.Stop()
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

	return r.store.RemoveInput(streamPath)
}

// Shutdown stops all active pullers and waits for them to complete.
func (r *Router) Shutdown() {
	r.mu.Lock()
	pullers := r.pullers
	r.pullers = make(map[string]*worker.Puller)
	r.mu.Unlock()

	for path, puller := range pullers {
		r.log.Info("Stopping puller", "stream_path", path)
		puller.Stop()
	}

	// Wait for all pullers to finish
	for path, puller := range pullers {
		select {
		case <-puller.Done():
		case <-time.After(10 * time.Second):
			r.log.Warn("Puller did not stop within timeout", "stream_path", path)
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

// OnPublish is called by the RTMP hub when a publisher connects.
// It validates the path and token, then activates the input.
func (r *Router) OnPublish(streamPath, token, remoteAddr string) error {
	in, ok := r.store.GetInput(streamPath)
	if !ok {
		return fmt.Errorf("stream path %q not registered", streamPath)
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
	r.store.UpdateInputStatus(streamPath, state.InputStatusStopped, "")
	r.log.Info("Publisher disconnected", "stream_path", streamPath)
}

func isNativeRTMP(url string) bool {
	return strings.HasPrefix(url, "rtmp://") || strings.HasPrefix(url, "rtmps://")
}
