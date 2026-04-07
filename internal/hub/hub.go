// Package hub provides a unified interface for media ingestion hubs.
// It supports both RTMP and RTSP protocols for receiving published streams.
package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"

	"github.com/bluenviron/gortmplib"
	"github.com/bluenviron/gortmplib/pkg/codecs"
)

// HubType represents the type of hub protocol.
type HubType string

const (
	HubTypeRTMP = HubType("rtmp")
	HubTypeRTSP = HubType("rtsp")
)

// Hub is the interface for media ingestion hubs.
// It handles protocol-specific handshakes and manages publishers/subscribers.
type Hub interface {
	// Start begins listening for incoming connections.
	Start() error

	// Stop gracefully shuts down the hub.
	Stop()

	// Addr returns the listener address.
	Addr() string

	// SetOnPublish sets the callback for publish events.
	SetOnPublish(handler func(streamPath, token, remoteAddr string) error)

	// SetOnUnpublish sets the callback for unpublish events.
	SetOnUnpublish(handler func(streamPath string))

	// EvictStream forcibly disconnects active publishers for a stream path.
	// This is used by control-plane reset operations (delete/import) to avoid
	// stale transport sessions surviving state removal.
	EvictStream(streamPath string)
}

// stream represents an active published stream with its tracks and subscribers.
type stream struct {
	publisher interface{} // *gortmplib.ServerConn for RTMP
	reader    interface{} // *gortmplib.Reader for RTMP
	tracks    []*gortmplib.Track
	writers   []*gortmplib.Writer
	mu        sync.Mutex
}

// RTMPHub manages RTMP connections and memory-based fan-out.
type RTMPHub struct {
	log         *logger.Logger
	addr        string
	listener    net.Listener
	mu          sync.RWMutex
	lifetimeMu  sync.Mutex // serializes Start/Stop to prevent WaitGroup reuse
	streams     map[string]*stream
	onPublish   func(streamPath, token, remoteAddr string) error
	onUnpublish func(streamPath string)
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewRTMPHub creates a new RTMP Hub.
func NewRTMPHub(log *logger.Logger, host string, port int) *RTMPHub {
	ctx, cancel := context.WithCancel(context.Background())
	return &RTMPHub{
		log:     log.With("component", "hub", "type", "rtmp"),
		addr:    fmt.Sprintf("%s:%d", host, port),
		streams: make(map[string]*stream),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SetOnPublish sets the callback for publish events.
func (h *RTMPHub) SetOnPublish(handler func(string, string, string) error) {
	h.onPublish = handler
}

// SetOnUnpublish sets the callback for unpublish events.
func (h *RTMPHub) SetOnUnpublish(handler func(string)) {
	h.onUnpublish = handler
}

// Start begins listening for RTMP connections.
// It is safe to call Start() again after Stop() to restart the hub.
func (h *RTMPHub) Start() error {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()

	// Renew the context if a previous Stop() cancelled it.
	select {
	case <-h.ctx.Done():
		h.ctx, h.cancel = context.WithCancel(context.Background())
	default:
	}

	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", h.addr, err)
	}

	h.mu.Lock()
	old := h.listener
	h.listener = ln
	h.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	h.log.Info("RTMP Hub listening", "addr", ln.Addr().String())

	h.wg.Add(1)
	go h.acceptLoop(ln, h.ctx)
	return nil
}

// acceptLoop accepts incoming connections until the listener is closed.
// ctx is captured from the Start() call that spawned this goroutine.
func (h *RTMPHub) acceptLoop(listener net.Listener, ctx context.Context) {
	defer h.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
				h.log.Error("Accept error", "error", err)
				continue
			}
		}
		h.wg.Add(1)
		go h.handleConn(conn)
	}
}

// handleConn processes a single RTMP connection through handshake and dispatch.
func (h *RTMPHub) handleConn(conn net.Conn) {
	defer h.wg.Done()
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	h.log.Debug("New RTMP connection", "remote", remoteAddr)

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	sc := &gortmplib.ServerConn{
		RW: conn,
	}

	if err := sc.Initialize(); err != nil {
		h.log.Error("RTMP handshake failed", "remote", remoteAddr, "error", err)
		return
	}

	if err := sc.Accept(); err != nil {
		h.log.Error("RTMP accept failed", "remote", remoteAddr, "error", err)
		return
	}

	streamPath, token := parseStreamURL(sc.URL)

	if sc.Publish {
		if err := h.handlePublisher(sc, conn, streamPath, token, remoteAddr); err != nil {
			if isExpectedConnClose(err) {
				h.log.Debug("Publisher disconnected", "path", streamPath, "remote", remoteAddr, "error", err)
			} else {
				h.log.Error("Publisher error", "path", streamPath, "remote", remoteAddr, "error", err)
			}
		}
	} else {
		if err := h.handleSubscriber(sc, conn, streamPath); err != nil {
			if isExpectedConnClose(err) {
				h.log.Debug("Subscriber disconnected", "path", streamPath, "remote", remoteAddr, "error", err)
			} else {
				h.log.Error("Subscriber error", "path", streamPath, "remote", remoteAddr, "error", err)
			}
		}
	}
}

// handlePublisher processes a publishing connection.
func (h *RTMPHub) handlePublisher(sc *gortmplib.ServerConn, conn net.Conn, streamPath, token, remoteAddr string) error {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	r := &gortmplib.Reader{
		Conn: sc,
	}
	if err := r.Initialize(); err != nil {
		return fmt.Errorf("reader init: %w", err)
	}

	s := &stream{
		publisher: sc,
		reader:    r,
		tracks:    r.Tracks(),
	}

	h.mu.Lock()
	if _, exists := h.streams[streamPath]; exists {
		h.mu.Unlock()
		return fmt.Errorf("stream %q already being published", streamPath)
	}
	h.streams[streamPath] = s
	h.mu.Unlock()

	if h.onPublish != nil {
		if err := h.onPublish(streamPath, token, remoteAddr); err != nil {
			h.mu.Lock()
			delete(h.streams, streamPath)
			h.mu.Unlock()
			return fmt.Errorf("publish rejected: %w", err)
		}
	}

	h.log.Info("Publisher connected",
		"path", streamPath,
		"remote", conn.RemoteAddr(),
		"tracks", len(s.tracks),
	)

	for _, track := range r.Tracks() {
		track := track
		switch track.Codec.(type) {
		case *codecs.H264:
			r.OnDataH264(track, func(pts, dts time.Duration, au [][]byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteH264(track, pts, dts, au) //nolint:errcheck
				}
			})
		case *codecs.H265:
			r.OnDataH265(track, func(pts, dts time.Duration, au [][]byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteH265(track, pts, dts, au) //nolint:errcheck
				}
			})
		case *codecs.AV1:
			r.OnDataAV1(track, func(pts time.Duration, tu [][]byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteAV1(track, pts, tu) //nolint:errcheck
				}
			})
		case *codecs.VP9:
			r.OnDataVP9(track, func(pts time.Duration, frame []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteVP9(track, pts, frame) //nolint:errcheck
				}
			})
		case *codecs.MPEG4Audio:
			r.OnDataMPEG4Audio(track, func(pts time.Duration, au []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteMPEG4Audio(track, pts, au) //nolint:errcheck
				}
			})
		case *codecs.MPEG1Audio:
			r.OnDataMPEG1Audio(track, func(pts time.Duration, frame []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteMPEG1Audio(track, pts, frame) //nolint:errcheck
				}
			})
		case *codecs.Opus:
			r.OnDataOpus(track, func(pts time.Duration, packet []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteOpus(track, pts, packet) //nolint:errcheck
				}
			})
		case *codecs.AC3:
			r.OnDataAC3(track, func(pts time.Duration, frame []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteAC3(track, pts, frame) //nolint:errcheck
				}
			})
		case *codecs.G711:
			r.OnDataG711(track, func(pts time.Duration, samples []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteG711(track, pts, samples) //nolint:errcheck
				}
			})
		case *codecs.LPCM:
			r.OnDataLPCM(track, func(pts time.Duration, samples []byte) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, w := range s.writers {
					w.WriteLPCM(track, pts, samples) //nolint:errcheck
				}
			})
		}
	}

	defer func() {
		h.mu.Lock()
		if existing, ok := h.streams[streamPath]; ok && existing.publisher == sc {
			s.mu.Lock()
			for _, w := range s.writers {
				w.Conn.(*gortmplib.ServerConn).RW.(net.Conn).Close()
			}
			s.mu.Unlock()
			delete(h.streams, streamPath)
		}
		h.mu.Unlock()

		if h.onUnpublish != nil {
			h.onUnpublish(streamPath)
		}
		h.log.Info("Publisher disconnected", "path", streamPath)
	}()

	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := r.Read(); err != nil {
			return err
		}
	}
}

// handleSubscriber processes a reading/subscribing connection.
func (h *RTMPHub) handleSubscriber(sc *gortmplib.ServerConn, conn net.Conn, streamPath string) error {
	h.mu.RLock()
	s, exists := h.streams[streamPath]
	h.mu.RUnlock()

	if !exists {
		return fmt.Errorf("stream %q not found", streamPath)
	}

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	w := &gortmplib.Writer{
		Conn:   sc,
		Tracks: s.tracks,
	}
	if err := w.Initialize(); err != nil {
		return fmt.Errorf("writer init: %w", err)
	}

	s.mu.Lock()
	s.writers = append(s.writers, w)
	s.mu.Unlock()

	h.log.Info("Subscriber connected", "path", streamPath, "remote", conn.RemoteAddr())

	defer func() {
		s.mu.Lock()
		s.writers = slices.DeleteFunc(s.writers, func(el *gortmplib.Writer) bool {
			return el == w
		})
		s.mu.Unlock()
		h.log.Info("Subscriber disconnected", "path", streamPath, "remote", conn.RemoteAddr())
	}()

	conn.SetReadDeadline(time.Time{})
	for {
		buf := make([]byte, 1024)
		_, err := sc.RW.Read(buf)
		if err != nil {
			return err
		}
	}
}

// EvictStream forcibly disconnects the current publisher (if any) for streamPath.
func (h *RTMPHub) EvictStream(streamPath string) {
	h.mu.Lock()
	s, exists := h.streams[streamPath]
	if exists {
		delete(h.streams, streamPath)
	}
	h.mu.Unlock()

	if !exists {
		return
	}

	if sc, ok := s.publisher.(*gortmplib.ServerConn); ok {
		if conn, ok := sc.RW.(net.Conn); ok {
			_ = conn.Close()
		}
	}

	s.mu.Lock()
	for _, w := range s.writers {
		if sc, ok := w.Conn.(*gortmplib.ServerConn); ok {
			if conn, ok := sc.RW.(net.Conn); ok {
				_ = conn.Close()
			}
		}
	}
	s.mu.Unlock()

	h.log.Info("Stream evicted", "path", streamPath)
}

// Stop gracefully shuts down the hub and waits for all goroutines to exit.
// Concurrent Stop() calls are serialized; the first one does the work and
// the rest are safe no-ops.
func (h *RTMPHub) Stop() {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()

	h.cancel()

	h.mu.Lock()
	ln := h.listener
	h.listener = nil
	h.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}

	h.mu.Lock()
	for path, s := range h.streams {
		s.publisher.(*gortmplib.ServerConn).RW.(net.Conn).Close()
		delete(h.streams, path)
	}
	h.mu.Unlock()
	h.wg.Wait()
	h.log.Info("RTMP Hub stopped")
}

// Addr returns the listener address.
func (h *RTMPHub) Addr() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.listener != nil {
		return h.listener.Addr().String()
	}
	return h.addr
}

// parseStreamURL extracts the stream path and optional token from the RTMP URL.
func parseStreamURL(u *url.URL) (streamPath, token string) {
	if u == nil {
		return "", ""
	}
	streamPath = strings.TrimPrefix(u.Path, "/")
	token = u.Query().Get("token")
	return streamPath, token
}

func isExpectedConnClose(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "closed network connection") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "read: eof")
}

// NewHub creates a hub of the specified type.
func NewHub(log *logger.Logger, hubType HubType, host string, port int) Hub {
	switch hubType {
	case HubTypeRTSP:
		return NewRTSPHub(log, host, port)
	default:
		return NewRTMPHub(log, host, port)
	}
}
