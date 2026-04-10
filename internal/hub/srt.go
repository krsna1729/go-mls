package hub

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"

	srt "github.com/datarhei/gosrt"
)

type srtChannel struct {
	pubsub    srt.PubSub
	publisher srt.Conn
}

type srtHub struct {
	log         *logger.Logger
	addr        string
	server      *srt.Server
	started     bool
	mu          sync.RWMutex
	lifetimeMu  sync.Mutex
	channels    map[string]*srtChannel
	onPublish   func(streamPath, token, remoteAddr string) error
	onUnpublish func(streamPath string)
}

func NewSRTHub(log *logger.Logger, host string, port int) *srtHub {
	if strings.TrimSpace(host) == "" {
		host = "0.0.0.0"
	}
	return &srtHub{
		log:      log.With("component", "hub", "type", "srt"),
		addr:     fmt.Sprintf("%s:%d", host, port),
		channels: make(map[string]*srtChannel),
	}
}

func (h *srtHub) SetOnPublish(handler func(string, string, string) error) {
	h.onPublish = handler
}

func (h *srtHub) SetOnUnpublish(handler func(string)) {
	h.onUnpublish = handler
}

func (h *srtHub) Start() error {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()

	cfg := srt.DefaultConfig()
	cfg.TransmissionType = "live"
	cfg.Latency = 120 * time.Millisecond
	cfg.ConnectionTimeout = 5 * time.Second
	cfg.PeerIdleTimeout = 10 * time.Second

	srv := &srt.Server{
		Addr:            h.addr,
		Config:          &cfg,
		HandleConnect:   h.handleConnect,
		HandlePublish:   h.handlePublish,
		HandleSubscribe: h.handleSubscribe,
	}

	if err := srv.Listen(); err != nil {
		return fmt.Errorf("SRT server listen: %w", err)
	}

	h.mu.Lock()
	h.server = srv
	h.started = true
	h.mu.Unlock()

	go func() {
		if err := srv.Serve(); err != nil && err != srt.ErrServerClosed {
			h.log.Error("SRT server stopped unexpectedly", "error", err)
		}
	}()

	h.log.Info("SRT Hub listening", "addr", h.Addr())
	return nil
}

func (h *srtHub) Stop() {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()

	h.mu.Lock()
	srv := h.server
	h.server = nil
	h.started = false
	h.channels = make(map[string]*srtChannel)
	h.mu.Unlock()

	if srv != nil {
		srv.Shutdown()
	}

	h.log.Info("SRT Hub stopped")
}

func (h *srtHub) Addr() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.addr
}

func (h *srtHub) EvictStream(streamPath string) {
	h.mu.Lock()
	channel, ok := h.channels[streamPath]
	if ok {
		delete(h.channels, streamPath)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	// Force-close publisher/subscriber graph for this stream.
	if channel.publisher != nil {
		channel.publisher.Close()
	}

	if h.onUnpublish != nil {
		h.onUnpublish(streamPath)
	}

	h.log.Info("SRT stream evicted", "path", streamPath)
}

func (h *srtHub) handleConnect(req srt.ConnRequest) srt.ConnType {
	mode, streamPath, token, ok := parseSRTStreamID(req.StreamId())
	if !ok {
		return srt.REJECT
	}

	remoteAddr := ""
	if req.RemoteAddr() != nil {
		remoteAddr = req.RemoteAddr().String()
	}

	h.mu.RLock()
	_, exists := h.channels[streamPath]
	h.mu.RUnlock()

	if mode == srt.PUBLISH {
		if exists {
			return srt.REJECT
		}
		if h.onPublish != nil {
			if err := h.onPublish(streamPath, token, remoteAddr); err != nil {
				h.log.Warn("SRT publish rejected", "path", streamPath, "remote_addr", remoteAddr, "error", err)
				return srt.REJECT
			}
		}
		h.mu.Lock()
		if _, ok := h.channels[streamPath]; !ok {
			h.channels[streamPath] = &srtChannel{pubsub: srt.NewPubSub(srt.PubSubConfig{})}
		}
		h.mu.Unlock()
		return srt.PUBLISH
	}

	if !exists {
		return srt.REJECT
	}

	return srt.SUBSCRIBE
}

func (h *srtHub) handlePublish(conn srt.Conn) {
	streamPath, _, _ := parseSRTConn(conn)
	if streamPath == "" {
		conn.Close()
		return
	}

	h.mu.Lock()
	channel := h.channels[streamPath]
	if channel == nil {
		channel = &srtChannel{pubsub: srt.NewPubSub(srt.PubSubConfig{})}
		h.channels[streamPath] = channel
	}
	channel.publisher = conn
	h.mu.Unlock()

	h.log.Info("SRT publisher connected", "path", streamPath, "remote_addr", conn.RemoteAddr())

	_ = channel.pubsub.Publish(conn)

	h.mu.Lock()
	delete(h.channels, streamPath)
	h.mu.Unlock()

	if h.onUnpublish != nil {
		h.onUnpublish(streamPath)
	}

	h.log.Info("SRT publisher disconnected", "path", streamPath)
	conn.Close()
}

func (h *srtHub) handleSubscribe(conn srt.Conn) {
	_, streamPath, _, ok := parseSRTStreamID(conn.StreamId())
	if !ok {
		conn.Close()
		return
	}

	h.mu.RLock()
	channel := h.channels[streamPath]
	h.mu.RUnlock()
	if channel == nil || channel.pubsub == nil {
		conn.Close()
		return
	}

	_ = channel.pubsub.Subscribe(conn)
	conn.Close()
}

func parseSRTConn(conn srt.Conn) (streamPath, token, remoteAddr string) {
	if conn == nil {
		return "", "", ""
	}
	_, streamPath, token, _ = parseSRTStreamID(conn.StreamId())
	if conn.RemoteAddr() != nil {
		remoteAddr = conn.RemoteAddr().String()
	}
	return streamPath, token, remoteAddr
}

func parseSRTStreamID(streamID string) (mode srt.ConnType, streamPath, token string, ok bool) {
	raw := strings.TrimSpace(streamID)
	if raw == "" {
		return srt.REJECT, "", "", false
	}

	mode = srt.PUBLISH
	payload := raw
	if strings.HasPrefix(raw, "publish:") {
		mode = srt.PUBLISH
		payload = strings.TrimPrefix(raw, "publish:")
	} else if strings.HasPrefix(raw, "subscribe:") {
		mode = srt.SUBSCRIBE
		payload = strings.TrimPrefix(raw, "subscribe:")
	}

	payload = strings.TrimSpace(payload)
	if payload == "" {
		return srt.REJECT, "", "", false
	}
	if !strings.HasPrefix(payload, "/") {
		payload = "/" + payload
	}

	u, err := url.Parse(payload)
	if err != nil {
		return srt.REJECT, "", "", false
	}
	streamPath = strings.TrimPrefix(strings.TrimSpace(u.Path), "/")
	if streamPath == "" {
		return srt.REJECT, "", "", false
	}
	token = u.Query().Get("token")

	return mode, streamPath, token, true
}
