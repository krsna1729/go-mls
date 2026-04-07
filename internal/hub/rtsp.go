package hub

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
)

type rtspStream struct {
	stream *gortsplib.ServerStream
	desc   *description.Session
	mu     sync.Mutex
}

type rtspHub struct {
	log         *logger.Logger
	addr        string
	boundAddr   string
	server      *gortsplib.Server
	started     bool
	mu          sync.RWMutex
	lifetimeMu  sync.Mutex // serializes Start/Stop to prevent concurrent lifecycle races
	streams     map[string]*rtspStream
	onPublish   func(streamPath, token, remoteAddr string) error
	onUnpublish func(streamPath string)
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewRTSPHub(log *logger.Logger, host string, port int) *rtspHub {
	if host == "" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &rtspHub{
		log:     log.With("component", "hub", "type", "rtsp"),
		addr:    fmt.Sprintf("%s:%d", host, port),
		streams: make(map[string]*rtspStream),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (h *rtspHub) SetOnPublish(handler func(string, string, string) error) {
	h.onPublish = handler
}

func (h *rtspHub) SetOnUnpublish(handler func(string)) {
	h.onUnpublish = handler
}

func (h *rtspHub) Start() error {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()

	// Renew lifecycle context on restart after Stop().
	select {
	case <-h.ctx.Done():
		h.ctx, h.cancel = context.WithCancel(context.Background())
	default:
	}

	srv := &gortsplib.Server{
		Handler:      h,
		RTSPAddress:  h.addr,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		Listen: func(network, address string) (net.Listener, error) {
			ln, err := net.Listen(network, address)
			if err != nil {
				return ln, err
			}
			h.mu.Lock()
			h.boundAddr = ln.Addr().String()
			h.mu.Unlock()
			return ln, nil
		},
	}

	if err := srv.Start(); err != nil {
		h.mu.Lock()
		h.started = false
		h.server = nil
		h.mu.Unlock()
		return fmt.Errorf("RTSP server start: %w", err)
	}

	h.mu.Lock()
	h.server = srv
	h.started = true
	h.mu.Unlock()

	h.log.Info("RTSP Hub listening", "addr", h.Addr())
	return nil
}

func (h *rtspHub) Stop() {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()

	h.cancel()

	h.mu.Lock()
	started := h.started
	srv := h.server
	h.started = false
	h.server = nil
	h.boundAddr = ""
	h.mu.Unlock()

	if started && srv != nil {
		srv.Close()
	}
	h.log.Info("RTSP Hub stopped")
}

func (h *rtspHub) Addr() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.boundAddr != "" {
		return h.boundAddr
	}
	return h.addr
}

func (h *rtspHub) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	h.mu.RLock()
	s, exists := h.streams[pathName]
	h.mu.RUnlock()

	if !exists || s.stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, s.stream, nil
}

func (h *rtspHub) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")

	if h.onPublish != nil {
		if err := h.onPublish(pathName, "", ""); err != nil {
			return &base.Response{StatusCode: base.StatusUnauthorized}, err
		}
	}

	stream := gortsplib.NewServerStream(h.server, ctx.Description)
	if err := stream.Initialize(); err != nil {
		return &base.Response{StatusCode: base.StatusInternalServerError}, err
	}

	h.mu.Lock()
	if existing, ok := h.streams[pathName]; ok && existing.stream != nil {
		existing.stream.Close()
	}
	h.streams[pathName] = &rtspStream{
		stream: stream,
		desc:   ctx.Description,
	}
	h.mu.Unlock()

	h.log.Info("RTSP publisher connected", "path", pathName, "tracks", len(ctx.Description.Medias))

	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *rtspHub) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")

	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		return &base.Response{StatusCode: base.StatusOK}, nil, nil
	}

	h.mu.RLock()
	s, exists := h.streams[pathName]
	h.mu.RUnlock()

	if !exists || s.stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, s.stream, nil
}

func (h *rtspHub) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	h.log.Info("RTSP client playing", "path", pathName)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *rtspHub) OnRecord(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")

	h.mu.RLock()
	s, ok := h.streams[pathName]
	h.mu.RUnlock()

	if ok && s.stream != nil {
		ctx.Session.OnPacketRTPAny(func(media *description.Media, f format.Format, pkt *rtp.Packet) {
			s.stream.WritePacketRTP(media, pkt) //nolint:errcheck
		})
	}

	h.log.Info("RTSP recording started", "path", pathName)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// EvictStream forcibly disconnects stream distribution for streamPath.
func (h *rtspHub) EvictStream(streamPath string) {
	h.mu.Lock()
	s, exists := h.streams[streamPath]
	if exists {
		delete(h.streams, streamPath)
	}
	h.mu.Unlock()

	if !exists {
		return
	}

	if s.stream != nil {
		s.stream.Close()
	}

	if h.onUnpublish != nil {
		h.onUnpublish(streamPath)
	}

	h.log.Info("RTSP stream evicted", "path", streamPath)
}
