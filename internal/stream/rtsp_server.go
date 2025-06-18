package stream

import (
	"context"
	"fmt"
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

// RTSP server configuration constants
const (
	DefaultRTSPPort      = 8554
	DefaultRTSPInterface = "127.0.0.1" // Listen locally by default
)

// GetRTSPServerURL returns the base RTSP server URL
func GetRTSPServerURL() string {
	return fmt.Sprintf("rtsp://%s:%d", DefaultRTSPInterface, DefaultRTSPPort)
}

// RTSPServerConfig contains the configuration for the RTSP server
type RTSPServerConfig struct {
	Port      int    `json:"port"`
	Interface string `json:"interface"`
}

// RTSPStreamInfo contains metadata about an RTSP stream
type RTSPStreamInfo struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	ClientCount   int       `json:"client_count"`
	BytesReceived int64     `json:"bytes_received"`
	StartTime     time.Time `json:"start_time"`
	Stream        *gortsplib.ServerStream
}

// RTSPServerManager manages the RTSP server instance
type RTSPServerManager struct {
	server       *gortsplib.Server
	config       RTSPServerConfig
	logger       *logger.Logger
	streams      map[string]*RTSPStreamInfo
	streamsMutex sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	streamReady  map[string]chan bool // Channel to signal when stream is ready for reading
}

// NewRTSPServerManager creates a new RTSP server manager
func NewRTSPServerManager(l *logger.Logger) *RTSPServerManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &RTSPServerManager{
		config: RTSPServerConfig{
			Port:      DefaultRTSPPort,
			Interface: DefaultRTSPInterface,
		},
		logger:      l,
		streams:     make(map[string]*RTSPStreamInfo),
		streamReady: make(map[string]chan bool),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start starts the RTSP server
func (rm *RTSPServerManager) Start() error {
	rm.logger.Info("Starting RTSP server on %s:%d", rm.config.Interface, rm.config.Port)

	// Create RTSP server instance with more permissive configuration
	rm.server = &gortsplib.Server{
		Handler:        rm,
		RTSPAddress:    fmt.Sprintf("%s:%d", rm.config.Interface, rm.config.Port),
		UDPRTPAddress:  fmt.Sprintf("%s:8000", rm.config.Interface),
		UDPRTCPAddress: fmt.Sprintf("%s:8001", rm.config.Interface),
		ReadTimeout:    5 * time.Second, // More generous timeouts
		WriteTimeout:   5 * time.Second,
	}

	// Start the server
	serverReady := make(chan bool, 1)
	go func() {
		err := rm.server.Start()
		if err != nil {
			rm.logger.Error("RTSP server error: %v", err)
			serverReady <- false
		} else {
			serverReady <- true
		}
	}()

	// Wait for server to be ready with timeout
	select {
	case ready := <-serverReady:
		if !ready {
			return fmt.Errorf("RTSP server failed to start")
		}
	case <-time.After(2 * time.Second):
		// Give it a moment to start, but don't block indefinitely
		rm.logger.Debug("RTSP server startup taking longer than expected, continuing...")
	}

	return nil
}

// Stop stops the RTSP server
func (rm *RTSPServerManager) Stop() {
	if rm.server != nil {
		rm.cancel()
		rm.server.Close()
		rm.logger.Info("RTSP server stopped")
	}
}

// OnDescribe is called when a client asks for stream information
func (rm *RTSPServerManager) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	rm.logger.Debug("RTSP OnDescribe: %s", pathName)

	rm.streamsMutex.Lock()
	streamInfo, ok := rm.streams[pathName]
	rm.streamsMutex.Unlock()

	// no one is publishing yet
	if !ok || streamInfo.Stream == nil {
		rm.logger.Debug("RTSP stream not found or not published yet: %s", pathName)
		return &base.Response{
			StatusCode: base.StatusNotFound,
		}, nil, nil
	}

	// send stream that is being published to the client
	return &base.Response{
		StatusCode: base.StatusOK,
	}, streamInfo.Stream, nil
}

// OnAnnounce is called when a client wants to publish a stream
func (rm *RTSPServerManager) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	rm.logger.Debug("RTSP OnAnnounce: %s", pathName)

	rm.streamsMutex.Lock()
	defer rm.streamsMutex.Unlock()

	// disconnect existing publisher if any
	if streamInfo, exists := rm.streams[pathName]; exists && streamInfo.Stream != nil {
		streamInfo.Stream.Close()
	}

	// create the stream and save it
	stream := &gortsplib.ServerStream{
		Server: rm.server,
		Desc:   ctx.Description,
	}
	err := stream.Initialize()
	if err != nil {
		rm.logger.Error("Failed to initialize RTSP stream: %v", err)
		return &base.Response{
			StatusCode: base.StatusInternalServerError,
		}, err
	}

	rm.streams[pathName] = &RTSPStreamInfo{
		Name:      pathName,
		Path:      ctx.Path,
		StartTime: time.Now(),
		Stream:    stream,
	}

	rm.logger.Info("Created RTSP stream: %s", ctx.Path)

	return &base.Response{
		StatusCode: base.StatusOK,
	}, nil
}

// OnSetup is called when a client sets up a stream transport
func (rm *RTSPServerManager) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	rm.logger.Debug("RTSP OnSetup: %s", pathName)

	// SETUP is used by both readers and publishers. In case of publishers, just return StatusOK.
	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		return &base.Response{
			StatusCode: base.StatusOK,
		}, nil, nil
	}

	rm.streamsMutex.Lock()
	streamInfo, ok := rm.streams[pathName]
	rm.streamsMutex.Unlock()

	// no one is publishing yet
	if !ok || streamInfo.Stream == nil {
		return &base.Response{
			StatusCode: base.StatusNotFound,
		}, nil, nil
	}

	return &base.Response{
		StatusCode: base.StatusOK,
	}, streamInfo.Stream, nil
}

// OnPlay is called when a client starts playing a stream
func (rm *RTSPServerManager) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	rm.logger.Debug("RTSP client started playing: %s", pathName)

	rm.streamsMutex.Lock()
	if streamInfo, ok := rm.streams[pathName]; ok {
		streamInfo.ClientCount++
	}
	rm.streamsMutex.Unlock()

	return &base.Response{
		StatusCode: base.StatusOK,
	}, nil
}

// OnRecord is called when a client starts recording (publishing) a stream
func (rm *RTSPServerManager) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	pathName := strings.TrimPrefix(ctx.Path, "/")
	rm.logger.Debug("RTSP client started recording: %s", pathName)

	rm.streamsMutex.Lock()
	streamInfo, ok := rm.streams[pathName]
	rm.streamsMutex.Unlock()

	if ok && streamInfo.Stream != nil {
		// called when receiving a RTP packet
		ctx.Session.OnPacketRTPAny(func(media *description.Media, _ format.Format, pkt *rtp.Packet) {
			// route the RTP packet to all readers
			streamInfo.Stream.WritePacketRTP(media, pkt) //nolint:errcheck
		})
	}

	// Signal that the stream is ready for reading after all setup is complete
	rm.streamsMutex.Lock()
	if readyChan, exists := rm.streamReady[pathName]; exists {
		select {
		case readyChan <- true:
			rm.logger.Debug("Signaled stream ready: %s", pathName)
		default:
			// Channel already has a value or is closed
		}
	}
	rm.streamsMutex.Unlock()

	return &base.Response{
		StatusCode: base.StatusOK,
	}, nil
}

// GetRTSPURL returns the RTSP URL for a stream name
func (rm *RTSPServerManager) GetRTSPURL(streamName string) string {
	return fmt.Sprintf("rtsp://%s:%d/%s", rm.config.Interface, rm.config.Port, streamName)
}

// GetStreamStats returns statistics for all active RTSP streams
func (rm *RTSPServerManager) GetStreamStats() []RTSPStreamInfo {
	rm.streamsMutex.Lock()
	defer rm.streamsMutex.Unlock()

	stats := make([]RTSPStreamInfo, 0, len(rm.streams))
	for _, stream := range rm.streams {
		// Create a copy without the stream reference
		stat := *stream
		stat.Stream = nil
		stats = append(stats, stat)
	}
	return stats
}

// CreateEmptyStream creates an RTSP stream path that can be published to
// We don't need to pre-create the stream in the latest gortsplib version,
// as streams are created dynamically when clients publish to them
func (rm *RTSPServerManager) CreateEmptyStream(name string) (string, error) {
	rm.streamsMutex.Lock()
	defer rm.streamsMutex.Unlock()

	// Check if stream already exists
	if _, exists := rm.streams[name]; exists {
		return rm.GetRTSPURL(name), nil
	}

	// Just register the stream name in our map for tracking
	rm.streams[name] = &RTSPStreamInfo{
		Name:      name,
		Path:      "/" + name,
		StartTime: time.Now(),
	}

	// Create a channel to signal when the stream is ready for reading
	rm.streamReady[name] = make(chan bool, 1)

	rm.logger.Info("Created RTSP stream path: %s", name)

	return rm.GetRTSPURL(name), nil
}

// WaitForStreamReady waits for a stream to become ready for reading (i.e., being published to)
func (rm *RTSPServerManager) WaitForStreamReady(name string, timeout time.Duration) error {
	// Use polling approach to handle multiple waiters correctly
	startTime := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond) // Check every 100ms
	defer ticker.Stop()

	for {
		// Check if stream is ready
		if rm.IsStreamReady(name) {
			rm.logger.Debug("Stream %s is ready for reading", name)
			return nil
		}

		// Check timeout
		if time.Since(startTime) >= timeout {
			return fmt.Errorf("timeout waiting for stream %s to become ready", name)
		}

		// Wait for next check
		select {
		case <-ticker.C:
			continue
		case <-time.After(timeout - time.Since(startTime)):
			return fmt.Errorf("timeout waiting for stream %s to become ready", name)
		}
	}
}

// IsStreamReady checks if a stream is ready for reading (non-blocking)
func (rm *RTSPServerManager) IsStreamReady(name string) bool {
	rm.streamsMutex.Lock()
	defer rm.streamsMutex.Unlock()

	streamInfo, exists := rm.streams[name]
	return exists && streamInfo.Stream != nil
}

// RemoveStream removes a stream
func (rm *RTSPServerManager) RemoveStream(name string) {
	rm.streamsMutex.Lock()
	defer rm.streamsMutex.Unlock()

	if streamInfo, exists := rm.streams[name]; exists {
		if streamInfo.Stream != nil {
			streamInfo.Stream.Close()
		}
		delete(rm.streams, name)
		rm.logger.Info("Removed RTSP stream: %s", name)
	}

	// Close and remove the ready channel
	if readyChan, exists := rm.streamReady[name]; exists {
		close(readyChan)
		delete(rm.streamReady, name)
	}
}
