// Package hub implements the RTMP Hub using bluenviron/gortmplib.
// It handles RTMP handshakes, publisher/subscriber management, and
// 1-to-N memory fan-out of audio/video data.
package hub

import (
	"context"
	"fmt"
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

// stream represents an active published stream with its tracks and subscribers.
type stream struct {
	publisher *gortmplib.ServerConn
	reader    *gortmplib.Reader
	tracks    []*gortmplib.Track
	writers   []*gortmplib.Writer
	mu        sync.Mutex
}

// Hub manages RTMP connections and memory-based fan-out.
type Hub struct {
	log         *logger.Logger
	addr        string
	listener    net.Listener
	mu          sync.RWMutex
	streams     map[string]*stream // path -> stream
	onPublish   func(streamPath, token string) error
	onUnpublish func(streamPath string)
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewHub creates a new RTMP Hub.
func NewHub(log *logger.Logger, host string, port int) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		log:     log.With("component", "hub"),
		addr:    fmt.Sprintf("%s:%d", host, port),
		streams: make(map[string]*stream),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SetHandlers sets callbacks for publish/unpublish events.
func (h *Hub) SetHandlers(onPublish func(string, string) error, onUnpublish func(string)) {
	h.onPublish = onPublish
	h.onUnpublish = onUnpublish
}

// Start begins listening for RTMP connections.
func (h *Hub) Start() error {
	var err error
	h.listener, err = net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", h.addr, err)
	}
	h.log.Info("RTMP Hub listening", "addr", h.listener.Addr().String())

	go h.acceptLoop()
	return nil
}

func (h *Hub) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			select {
			case <-h.ctx.Done():
				return
			default:
				h.log.Error("Accept error", "error", err)
				continue
			}
		}
		go h.handleConn(conn)
	}
}

// handleConn processes a single RTMP connection through handshake and dispatch.
func (h *Hub) handleConn(conn net.Conn) {
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

	// Extract stream path and token from URL
	streamPath, token := parseStreamURL(sc.URL)

	if sc.Publish {
		if err := h.handlePublisher(sc, conn, streamPath, token); err != nil {
			h.log.Error("Publisher error", "path", streamPath, "remote", remoteAddr, "error", err)
		}
	} else {
		if err := h.handleSubscriber(sc, conn, streamPath); err != nil {
			h.log.Error("Subscriber error", "path", streamPath, "remote", remoteAddr, "error", err)
		}
	}
}

// handlePublisher processes a publishing connection.
func (h *Hub) handlePublisher(sc *gortmplib.ServerConn, conn net.Conn, streamPath, token string) error {
	// Validate via ingest router (token gatekeeping)
	if h.onPublish != nil {
		if err := h.onPublish(streamPath, token); err != nil {
			return fmt.Errorf("publish rejected: %w", err)
		}
	}

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	r := &gortmplib.Reader{
		Conn: sc,
	}
	if err := r.Initialize(); err != nil {
		return fmt.Errorf("reader init: %w", err)
	}

	// Create stream entry
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

	h.log.Info("Publisher connected",
		"path", streamPath,
		"remote", conn.RemoteAddr(),
		"tracks", len(s.tracks),
	)

	// Setup data callbacks for fan-out to all subscribers
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

	// Cleanup on disconnect
	defer func() {
		h.mu.Lock()
		if existing, ok := h.streams[streamPath]; ok && existing.publisher == sc {
			// Close all subscriber connections
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

	// Read loop — blocks until publisher disconnects
	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := r.Read(); err != nil {
			return err
		}
	}
}

// handleSubscriber processes a reading/subscribing connection.
func (h *Hub) handleSubscriber(sc *gortmplib.ServerConn, conn net.Conn, streamPath string) error {
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

	// Add to subscriber list
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

	// Block until connection closes — subscriber just reads to detect disconnect
	conn.SetReadDeadline(time.Time{})
	for {
		buf := make([]byte, 1024)
		_, err := sc.RW.Read(buf)
		if err != nil {
			return err
		}
	}
}

// Stop gracefully shuts down the hub.
func (h *Hub) Stop() {
	h.cancel()
	if h.listener != nil {
		h.listener.Close()
	}
	// Close all publisher connections
	h.mu.Lock()
	for path, s := range h.streams {
		s.publisher.RW.(net.Conn).Close()
		delete(h.streams, path)
	}
	h.mu.Unlock()
	h.log.Info("RTMP Hub stopped")
}

// Addr returns the listener address (useful for tests with port 0).
func (h *Hub) Addr() string {
	if h.listener != nil {
		return h.listener.Addr().String()
	}
	return h.addr
}

// parseStreamURL extracts the stream path and optional token from the RTMP URL.
// e.g., rtmp://server/live/mycam?token=XYZ → path="live/mycam", token="XYZ"
func parseStreamURL(u *url.URL) (streamPath, token string) {
	if u == nil {
		return "", ""
	}
	// Path typically starts with / and may include the app name
	streamPath = strings.TrimPrefix(u.Path, "/")
	token = u.Query().Get("token")
	return streamPath, token
}
