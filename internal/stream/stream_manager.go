package stream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/process"
)

// FFmpegOptions defines configuration for FFmpeg transcoding
type FFmpegOptions struct {
	VideoCodec string   `json:"video_codec,omitempty"`
	AudioCodec string   `json:"audio_codec,omitempty"`
	Resolution string   `json:"resolution,omitempty"`
	Framerate  string   `json:"framerate,omitempty"`
	Bitrate    string   `json:"bitrate,omitempty"`
	Rotation   string   `json:"rotation,omitempty"`
	ExtraArgs  []string `json:"extra_args,omitempty"`
}

// PlatformPreset defines a named set of FFmpeg options
type PlatformPreset struct {
	Name    string        `json:"name"`
	Options FFmpegOptions `json:"options"`
}

// PlatformPresets contains predefined configurations for common platforms
var PlatformPresets = map[string]PlatformPreset{
	"YouTube": {
		Name: "YouTube",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "30",
			Bitrate:    "4500k",
		},
	},
	"Facebook": {
		Name: "Facebook",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1280x720",
			Framerate:  "30",
			Bitrate:    "2500k",
		},
	},
	"Twitch": {
		Name: "Twitch",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "60",
			Bitrate:    "6000k",
		},
	},
	"Instagram": {
		Name: "Instagram",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "720x1280",
			Framerate:  "30",
			Bitrate:    "3500k",
			Rotation:   "transpose=1",
		},
	},
	"Custom": {
		Name:    "Custom",
		Options: FFmpegOptions{},
	},
}

// StreamManager is the central orchestrator for all streaming operations.
// It replaces the legacy RelayManager and provides a unified facade for
// Input Relays, Output Relays, Recordings, and HLS.
type StreamManager struct {
	// Sub-managers
	InputRelays      *InputRelayManager
	OutputRelays     *OutputRelayManager
	RecordingManager *RecordingManager
	HLSManager       *HLSManager
	RTSPServer       *RTSPServerManager

	Logger *logger.Logger
	recDir string // Directory for playing recordings from (legacy relay support)

	// Configurable timeouts
	outputTimeout time.Duration

	// ffmpeg loglevel
	ffmpegLogLevel string

	// Mutex map for serializing concurrent starts of the same input URL
	startMutexes   map[string]*sync.Mutex
	startMutexesMu sync.Mutex

	// Consumer registry for tracking all active consumers
	consumerRegistry *ConsumerRegistry

	// status caching
	statusCache   StreamStatus
	statusCacheTs time.Time
	statusCacheMu sync.Mutex
}

const statusCacheTTL = 500 * time.Millisecond

// NewStreamManager creates a new StreamManager.
func NewStreamManager(l *logger.Logger, recDir string, ffmpegLogLevel string) *StreamManager {
	irm := NewInputRelayManager(l, recDir)
	orm := NewOutputRelayManager(l)
	sm := &StreamManager{
		InputRelays:      irm,
		OutputRelays:     orm,
		Logger:           l,
		recDir:           recDir,
		outputTimeout:    60 * time.Second,
		startMutexes:     make(map[string]*sync.Mutex),
		ffmpegLogLevel:   ffmpegLogLevel,
		consumerRegistry: NewConsumerRegistry(),
	}

	// Set up Consumer cleanup handler for output relay failures
	// When an output fails, it will call sm.OnConsumerFailure via dependency injection
	orm.SetCleanupHandler(sm) // StreamManager implements ConsumerCleanupHandler

	return sm
}

// Setters for dependencies (to handle initialization cycles if needed, though NewContext handles most)

// UnregisterConsumer unregisters a consumer and decrements the input reference count.
func (sm *StreamManager) UnregisterConsumer(inputURL, consumerID string) bool {
	unregistered := sm.consumerRegistry.Unregister(inputURL, consumerID)
	if unregistered {
		sm.Logger.Debug("Unregistered consumer", "inputURL", inputURL, "consumerID", consumerID)
		// Decrement refcount since consumer is removed
		sm.InputRelays.DecrementInputRef(inputURL, "consumer-"+consumerID)
	}
	return unregistered
}

func (sm *StreamManager) SetRTSPServer(server *RTSPServerManager) {
	sm.RTSPServer = server
	sm.InputRelays.SetRTSPServer(server)
}

func (sm *StreamManager) SetHLSManager(hls *HLSManager) {
	sm.HLSManager = hls
}

func (sm *StreamManager) SetRecordingManager(rec *RecordingManager) {
	sm.RecordingManager = rec
}

func (sm *StreamManager) SetTimeouts(inputTimeout, outputTimeout time.Duration) {
	sm.InputRelays.SetInputTimeout(inputTimeout)
	sm.outputTimeout = outputTimeout
	sm.Logger.Debug("StreamManager: Updated timeouts", "inputTimeout", inputTimeout, "outputTimeout", outputTimeout)
}

// OnConsumerFailure implements ConsumerCleanupHandler.
// This is called by consumers when they fail, providing automatic cleanup.
func (sm *StreamManager) OnConsumerFailure(inputURL, consumerID string) error {
	sm.Logger.Info("Handling consumer failure", "inputURL", inputURL, "consumerID", consumerID)

	// Unregister from registry
	unregistered := sm.consumerRegistry.Unregister(inputURL, consumerID)
	if !unregistered {
		sm.Logger.Warn("Consumer not found in registry during failure", "consumerID", consumerID)
		return nil
	}

	sm.Logger.Debug("Unregistered failed consumer from registry", "inputURL", inputURL, "consumerID", consumerID)

	// Decrement refcount - input will auto-stop if refcount reaches 0
	sm.InputRelays.DecrementInputRef(inputURL, "consumer-failure-"+consumerID)

	return nil
}

// --- Unified Status API ---

// StreamStatus represents the complete state of the streaming system.
type StreamStatus struct {
	Server     ServerStatus       `json:"server"`
	Relays     []RelayStatusV2    `json:"relays"`     // Grouped Input+Outputs
	Recordings []*Recording       `json:"recordings"` // Active and finished recordings
	HLS        []HLSSessionStatus `json:"hls"`        // HLS Sessions
}

type HLSSessionStatus struct {
	InputName  string    `json:"input_name"`
	Viewers    int       `json:"viewers"`
	Ready      bool      `json:"ready"`
	LastAccess time.Time `json:"last_access"`
}

type ServerStatus struct {
	CPU float64 `json:"cpu"`
	Mem uint64  `json:"mem"`
}

type RelayStatusV2 struct {
	Input   InputRelayStatusV2    `json:"input"`
	Outputs []OutputRelayStatusV2 `json:"outputs"`
}

type InputRelayStatusV2 struct {
	InputURL  string  `json:"input_url"`
	InputName string  `json:"input_name"`
	LocalURL  string  `json:"local_url"`
	Status    string  `json:"status"`
	LastError string  `json:"last_error,omitempty"`
	CPU       float64 `json:"cpu"`
	Mem       uint64  `json:"mem"`
	Speed     float64 `json:"speed"`
}

type OutputRelayStatusV2 struct {
	OutputURL  string  `json:"output_url"`
	OutputName string  `json:"output_name"`
	InputURL   string  `json:"input_url"`
	LocalURL   string  `json:"local_url"`
	Status     string  `json:"status"`
	LastError  string  `json:"last_error,omitempty"`
	CPU        float64 `json:"cpu"`
	Mem        uint64  `json:"mem"`
	Bitrate    float64 `json:"bitrate"`
}

func inputRelayStatusString(s InputRelayStatus) string {
	switch s {
	case InputStopped:
		return "Stopped"
	case InputStarting:
		return "Starting"
	case InputRunning:
		return "Running"
	case InputError:
		return "Error"
	default:
		return "Unknown"
	}
}

func outputRelayStatusString(s OutputRelayStatus) string {
	switch s {
	case OutputStopped:
		return "Stopped"
	case OutputStarting:
		return "Starting"
	case OutputRunning:
		return "Running"
	case OutputError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Status returns the unified system status.
func (sm *StreamManager) Status() StreamStatus {
	// Return cached snapshot if fresh
	sm.statusCacheMu.Lock()
	if !sm.statusCacheTs.IsZero() && time.Since(sm.statusCacheTs) < statusCacheTTL {
		snapshot := sm.statusCache
		sm.statusCacheMu.Unlock()
		return snapshot
	}
	sm.statusCacheMu.Unlock()

	// 1. Server Stats
	srv, _ := process.GetSelfUsage()
	serverStatus := ServerStatus{}
	if srv != nil {
		serverStatus = ServerStatus{CPU: srv.CPU, Mem: srv.Mem}
	}

	// 2. Relay Stats (Logic ported from RelayManager.StatusV2)
	relays := make([]RelayStatusV2, 0)

	sm.InputRelays.mu.Lock()
	inputs := make([]*InputRelay, 0, len(sm.InputRelays.Relays))
	for _, in := range sm.InputRelays.Relays {
		inputs = append(inputs, in)
	}
	sm.InputRelays.mu.Unlock()

	sm.OutputRelays.mu.Lock()
	outputsAll := make([]*OutputRelay, 0, len(sm.OutputRelays.Relays))
	for _, out := range sm.OutputRelays.Relays {
		outputsAll = append(outputsAll, out)
	}
	sm.OutputRelays.mu.Unlock()

	for _, in := range inputs {
		in.mu.Lock()
		cpu, mem := 0.0, uint64(0)
		if in.Proc != nil {
			if pid := in.Proc.GetPID(); pid > 0 {
				if usage, err := process.GetProcUsage(pid); err == nil {
					cpu = usage.CPU
					mem = usage.Mem
				}
			}
		}
		inputStatus := InputRelayStatusV2{
			InputURL:  in.InputURL,
			InputName: in.InputName,
			LocalURL:  in.LocalURL,
			Status:    inputRelayStatusString(in.Status),
			LastError: in.LastError,
			CPU:       cpu,
			Mem:       mem,
		}
		if in.Proc != nil {
			inputStatus.Speed, _ = in.Proc.GetSpeed()
		}

		outputs := make([]OutputRelayStatusV2, 0)
		for _, out := range outputsAll {
			if out.InputURL != in.InputURL {
				continue
			}
			out.mu.Lock()
			cpuO, memO := 0.0, uint64(0)
			if out.Proc != nil {
				if pid := out.Proc.GetPID(); pid > 0 {
					if usage, err := process.GetProcUsage(pid); err == nil {
						cpuO = usage.CPU
						memO = usage.Mem
					}
				}
			}
			outputStatus := OutputRelayStatusV2{
				OutputURL:  out.OutputURL,
				OutputName: out.OutputName,
				InputURL:   out.InputURL,
				LocalURL:   out.LocalURL,
				Status:     outputRelayStatusString(out.Status),
				LastError:  out.LastError,
				CPU:        cpuO,
				Mem:        memO,
			}
			if out.Proc != nil {
				if bitrate, ok := out.Proc.GetBitrate(); ok {
					outputStatus.Bitrate = bitrate
				}
			}
			outputs = append(outputs, outputStatus)
			out.mu.Unlock()
		}

		relays = append(relays, RelayStatusV2{
			Input:   inputStatus,
			Outputs: outputs,
		})
		in.mu.Unlock()
	}

	// 3. Recording Stats
	var recordings []*Recording
	if sm.RecordingManager != nil {
		recordings = sm.RecordingManager.ListRecordings()
	}

	// 4. HLS Stats
	hlsStats := make([]HLSSessionStatus, 0)
	if sm.HLSManager != nil {
		sm.HLSManager.mu.RLock()
		for name, sess := range sm.HLSManager.sessions {
			sess.Mu.RLock()
			hlsStats = append(hlsStats, HLSSessionStatus{
				InputName:  name,
				Viewers:    len(sess.ViewerIDs),
				Ready:      sess.Ready,
				LastAccess: sess.LastAccess,
			})
			sess.Mu.RUnlock()
		}
		sm.HLSManager.mu.RUnlock()
	}

	resp := StreamStatus{
		Server:     serverStatus,
		Relays:     relays,
		Recordings: recordings,
		HLS:        hlsStats,
	}

	// Cache
	sm.statusCacheMu.Lock()
	sm.statusCache = resp
	sm.statusCacheTs = time.Now()
	sm.statusCacheMu.Unlock()

	return resp
}

// --- Relay Management Methods (Ported) ---

// StartStream starts a relay stream (Input -> Output).
// Replaces StartRelayWithOptions.
func (sm *StreamManager) StartStream(inputURL, outputURL, inputName, outputName string, opts *FFmpegOptions, preset string) error {
	sm.Logger.Debug("StartStream called", "inputName", inputName, "outputName", outputName)

	// Register input config
	sm.InputRelays.RegisterInputConfig(inputName, inputURL)

	// Serialize starts
	startMutex := sm.getStartMutex(inputURL)
	startMutex.Lock()
	defer startMutex.Unlock()

	// Get input stream (starts input relay if needed)
	localRelayURL, err := sm.InputRelays.GetStream(inputName)
	if err != nil {
		return err
	}

	// Build FFmpeg args
	loglevel := sm.ffmpegLogLevel
	if loglevel == "" {
		loglevel = "info"
	}
	args := []string{"-hide_banner", "-loglevel", loglevel, "-stats", "-re", "-i", localRelayURL}

	if opts != nil {
		if opts.VideoCodec != "" {
			args = append(args, "-c:v", opts.VideoCodec)
		}
		if opts.AudioCodec != "" {
			args = append(args, "-c:a", opts.AudioCodec)
		}
		if opts.Resolution != "" {
			args = append(args, "-s", opts.Resolution)
		}
		if opts.Framerate != "" {
			args = append(args, "-r", opts.Framerate)
		}
		if opts.Bitrate != "" {
			args = append(args, "-b:v", opts.Bitrate)
		}
		if opts.Rotation != "" {
			args = append(args, "-vf", opts.Rotation)
		}
		if len(opts.ExtraArgs) > 0 {
			args = append(args, opts.ExtraArgs...)
		}
	}

	// Resolve output URL
	resolvedOutputURL := outputURL
	if strings.HasPrefix(outputURL, "file://") {
		relativePath := strings.TrimPrefix(outputURL, "file://")
		resolvedOutputURL = filepath.Join(sm.recDir, relativePath)
	}

	args = append(args, "-f", "flv", resolvedOutputURL)

	// Convert options for storage
	var optsMap map[string]string
	if opts != nil {
		optsMap = map[string]string{
			"video_codec": opts.VideoCodec,
			"audio_codec": opts.AudioCodec,
			"resolution":  opts.Resolution,
			"framerate":   opts.Framerate,
			"bitrate":     opts.Bitrate,
			"rotation":    opts.Rotation,
		}
	}

	config := OutputRelayConfig{
		OutputURL:      outputURL,
		OutputName:     outputName,
		InputURL:       inputURL,
		LocalURL:       localRelayURL,
		Timeout:        sm.outputTimeout,
		PlatformPreset: preset,
		FFmpegOptions:  optsMap,
		FFmpegArgs:     args,
	}

	if err := sm.OutputRelays.StartOutputRelay(config); err != nil {
		sm.InputRelays.ReleaseStream(inputName)
		return err
	}

	// Register consumer in registry and store in relay
	// Get the output relay that was just created
	sm.OutputRelays.mu.Lock()
	outputRelay, exists := sm.OutputRelays.Relays[outputURL]
	sm.OutputRelays.mu.Unlock()

	if exists {
		consumer := NewOutputRelayConsumer(outputRelay, sm.OutputRelays, inputURL)

		// Store consumer in relay for failure handling
		outputRelay.mu.Lock()
		outputRelay.consumer = consumer
		outputRelay.mu.Unlock()

		// Register in consumer registry
		sm.consumerRegistry.Register(inputURL, consumer)
		sm.Logger.Debug("Registered output consumer", "inputURL", inputURL, "outputURL", outputURL, "consumerID", consumer.GetConsumerID())
	}

	sm.Logger.Info("Started stream relay", "inputName", inputName, "outputName", outputName)
	return nil
}

// StopStream stops a relay stream.
func (sm *StreamManager) StopStream(inputURL, outputURL, inputName, outputName string) error {
	sm.Logger.Debug("StopStream called", "inputName", inputName, "outputName", outputName)

	// Unregister consumer from registry (this also decrements refcount)
	sm.UnregisterConsumer(inputURL, outputURL)

	sm.OutputRelays.StopOutputRelay(outputURL)
	return nil
}

// DeleteInput deletes an input and all associated outputs/recordings/HLS.
func (sm *StreamManager) DeleteInput(inputURL, inputName string) error {
	sm.Logger.Debug("DeleteInput called", "inputName", inputName)

	// 1. Delete associated outputs (must unregister consumers first)
	sm.OutputRelays.mu.Lock()
	var outputsToDelete []struct {
		outputURL string
		inputURL  string
	}
	for outURL, relay := range sm.OutputRelays.Relays {
		if relay.InputURL == inputURL {
			outputsToDelete = append(outputsToDelete, struct {
				outputURL string
				inputURL  string
			}{outURL, relay.InputURL})
		}
	}
	sm.OutputRelays.mu.Unlock()

	for _, output := range outputsToDelete {
		// Unregister consumer first to decrement refcount
		sm.UnregisterConsumer(output.inputURL, output.outputURL)
		// Then delete the output
		sm.OutputRelays.DeleteOutput(output.outputURL)
	}

	// 2. Stop recordings
	if sm.RecordingManager != nil {
		sm.RecordingManager.StopRecording(inputName, inputURL)
	}

	// 3. Delete HLS session
	if sm.HLSManager != nil {
		sm.HLSManager.DeleteSession(inputName)
	}

	// 4. Delete Input Relay
	return sm.InputRelays.DeleteInput(inputURL)
}

// DeleteOutput deletes a single output relay.
func (sm *StreamManager) DeleteOutput(inputURL, outputURL, inputName, outputName string) error {
	sm.Logger.Debug("DeleteOutput called", "outputName", outputName)

	// Unregister consumer first (this also decrements refcount)
	sm.UnregisterConsumer(inputURL, outputURL)

	// Stop if running
	sm.OutputRelays.mu.Lock()
	out, exists := sm.OutputRelays.Relays[outputURL]
	sm.OutputRelays.mu.Unlock()

	if exists {
		out.mu.Lock()
		running := out.Status == OutputRunning || out.Status == OutputStarting
		out.mu.Unlock()
		if running {
			sm.OutputRelays.StopOutputRelay(outputURL)
			// sm.InputRelays.StopInputRelay(inputURL) - Removed to avoid double decrement
		}
	}

	return sm.OutputRelays.DeleteOutput(outputURL)
}

// Shutdown stops all activities.
func (sm *StreamManager) Shutdown() {
	sm.Logger.Info("StreamManager: Shutting down all relays...")

	// Stop outputs
	sm.OutputRelays.mu.Lock()
	var toStop []struct{ in, out, name string }
	for _, out := range sm.OutputRelays.Relays {
		out.mu.Lock()
		if out.Status == OutputRunning || out.Status == OutputStarting {
			toStop = append(toStop, struct{ in, out, name string }{out.InputURL, out.OutputURL, out.OutputName})
		}
		out.mu.Unlock()
	}
	sm.OutputRelays.mu.Unlock()

	for _, s := range toStop {
		sm.StopStream(s.in, s.out, "", s.name)
	}

	// Force cleanup inputs if needed
	sm.InputRelays.mu.Lock()
	for url, in := range sm.InputRelays.Relays {
		in.mu.Lock()
		if in.Status != InputStopped {
			sm.Logger.Warn("Force stopping input relay", "url", url)
			sm.InputRelays.ForceStopInputRelay(url)
		}
		in.mu.Unlock()
	}
	sm.InputRelays.mu.Unlock()
}

// GetEndpointConfig retrieves config for an output.
func (sm *StreamManager) GetEndpointConfig(inputURL, outputURL string) (string, *FFmpegOptions, error) {
	sm.OutputRelays.mu.Lock()
	out, exists := sm.OutputRelays.Relays[outputURL]
	sm.OutputRelays.mu.Unlock()

	if !exists || out.InputURL != inputURL {
		return "", nil, fmt.Errorf("output not found")
	}

	var opts *FFmpegOptions
	if out.FFmpegOptions != nil {
		opts = &FFmpegOptions{
			VideoCodec: out.FFmpegOptions["video_codec"],
			AudioCodec: out.FFmpegOptions["audio_codec"],
			Resolution: out.FFmpegOptions["resolution"],
			Framerate:  out.FFmpegOptions["framerate"],
			Bitrate:    out.FFmpegOptions["bitrate"],
			Rotation:   out.FFmpegOptions["rotation"],
		}
	}
	return out.PlatformPreset, opts, nil
}

// ExportConfig exports relay config.
func (sm *StreamManager) ExportConfig(filename string) error {
	// Reusing logic from RelayManager, simplified
	type exportOutput struct {
		OutputURL      string            `json:"output_url"`
		OutputName     string            `json:"output_name"`
		PlatformPreset string            `json:"platform_preset,omitempty"`
		FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
	}
	type exportConfig struct {
		InputURL  string         `json:"input_url"`
		InputName string         `json:"input_name"`
		Outputs   []exportOutput `json:"outputs"`
	}

	var configs []exportConfig
	runningInputs := sm.InputRelays.GetRunningInputRelays()

	for _, in := range runningInputs {
		var outputs []exportOutput
		sm.OutputRelays.mu.Lock()
		for outURL, out := range sm.OutputRelays.Relays {
			out.mu.Lock()
			if out.InputURL == in.InputURL {
				outputs = append(outputs, exportOutput{
					OutputURL:      outURL,
					OutputName:     out.OutputName,
					PlatformPreset: out.PlatformPreset,
					FFmpegOptions:  out.FFmpegOptions,
				})
			}
			out.mu.Unlock()
		}
		sm.OutputRelays.mu.Unlock()

		configs = append(configs, exportConfig{
			InputURL:  in.InputURL,
			InputName: in.InputName,
			Outputs:   outputs,
		})
	}

	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// ImportConfig imports relay config.
func (sm *StreamManager) ImportConfig(filename string) error {
	type importConfig struct {
		InputURL  string `json:"input_url"`
		InputName string `json:"input_name"`
		Outputs   []struct {
			OutputURL      string            `json:"output_url"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset,omitempty"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
		} `json:"outputs"`
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var configs []importConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, cfg := range configs {
		sm.InputRelays.RegisterInputConfig(cfg.InputName, cfg.InputURL)
		for _, out := range cfg.Outputs {
			wg.Add(1)
			go func(inURL, inName, outURL, outName, preset string, optsMap map[string]string) {
				defer wg.Done()
				opts, _ := sm.applyPresetAndOptions(preset, optsMap, "", "")
				sm.StartStream(inURL, outURL, inName, outName, opts, preset)
			}(cfg.InputURL, cfg.InputName, out.OutputURL, out.OutputName, out.PlatformPreset, out.FFmpegOptions)
		}
	}
	wg.Wait()
	return nil
}

// Helper: getStartMutex
func (sm *StreamManager) getStartMutex(inputURL string) *sync.Mutex {
	sm.startMutexesMu.Lock()
	defer sm.startMutexesMu.Unlock()
	if m, ok := sm.startMutexes[inputURL]; ok {
		return m
	}
	m := &sync.Mutex{}
	sm.startMutexes[inputURL] = m
	return m
}
