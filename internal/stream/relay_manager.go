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

// RelayManager manages all relays (per input URL)
type RelayManager struct {
	InputRelays  *InputRelayManager
	OutputRelays *OutputRelayManager
	Logger       *logger.Logger
	rtspServer   *RTSPServerManager // RTSP server for local relays
	recDir       string             // Directory for playing recordings from

	// Add references for HLSManager and RecordingManager
	HLSManager       *HLSManager
	RecordingManager *RecordingManager

	// Configurable timeouts
	outputTimeout time.Duration

	// ffmpeg loglevel (configurable)
	ffmpegLogLevel string

	// Mutex map for serializing concurrent starts of the same input URL
	startMutexes   map[string]*sync.Mutex
	startMutexesMu sync.Mutex

	// status caching to avoid expensive per-request sampling
	statusCache   StatusV2Response
	statusCacheTs time.Time
	statusCacheMu sync.Mutex
}

const statusCacheTTL = 500 * time.Millisecond

func NewRelayManager(l *logger.Logger, recDir string, ffmpegLogLevel string) *RelayManager {
	irm := NewInputRelayManager(l, recDir)
	orm := NewOutputRelayManager(l)
	rm := &RelayManager{
		InputRelays:    irm,
		OutputRelays:   orm,
		Logger:         l,
		recDir:         recDir,
		outputTimeout:  60 * time.Second,
		startMutexes:   make(map[string]*sync.Mutex),
		ffmpegLogLevel: ffmpegLogLevel,
	}

	// Set up failure callback for output relays to clean up input relay refcount
	orm.SetFailureCallback(func(inputURL, outputURL string) {
		l.Info("Output relay failure callback: cleaning up input relay refcount for", "inputURL", inputURL, "outputURL", outputURL)
		irm.DecrementInputRef(inputURL, "failureCallback")
	})

	return rm
}

// SetRTSPServer sets the RTSP server instance
func (rm *RelayManager) SetRTSPServer(server *RTSPServerManager) {
	rm.rtspServer = server
	rm.InputRelays.SetRTSPServer(server) // Also set it on InputRelayManager for cleanup
}

// GetRTSPServer returns the RTSP server instance
func (rm *RelayManager) GetRTSPServer() *RTSPServerManager {
	return rm.rtspServer
}

// FFmpegOptions allows advanced control over output
// (codec, resolution, rotation, etc.)
type FFmpegOptions struct {
	VideoCodec string // e.g. "libx264", "libx265"
	AudioCodec string // e.g. "aac", "mp3"
	Resolution string // e.g. "1280x720"
	Framerate  string // e.g. "30"
	Bitrate    string // e.g. "2500k"
	Rotation   string // e.g. "transpose=1" for 90deg
	ExtraArgs  []string
}

// PlatformPreset defines a set of FFmpeg options for a platform
// (YouTube, Instagram, TikTok, etc.)
type PlatformPreset struct {
	Name    string
	Options FFmpegOptions
}

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
	"TikTok": {
		Name: "TikTok",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "720x1280",
			Framerate:  "30",
			Bitrate:    "2500k",
			Rotation:   "transpose=1",
		},
	},
}

// StartRelay starts a relay for an input/output URL and stores names
// StartRelayWithOptions starts a relay with advanced ffmpeg options and/or platform preset
func (rm *RelayManager) StartRelayWithOptions(inputURL, outputURL, inputName, outputName string, opts *FFmpegOptions, preset string) error {
	rm.Logger.Debug("StartRelayWithOptions called", "inputURL", inputURL, "outputURL", outputURL, "inputName", inputName, "outputName", outputName, "preset", preset)

	// Register input configuration for future HLS access
	rm.InputRelays.RegisterInputConfig(inputName, inputURL)

	// Get mutex for this input URL to serialize concurrent starts
	startMutex := rm.getStartMutex(inputURL)
	startMutex.Lock()
	defer startMutex.Unlock()

	// Use GetStream to ensure proper input relay lifecycle (waits until ready)
	localRelayURL, err := rm.InputRelays.GetStream(inputName)
	if err != nil {
		rm.Logger.Error("Failed to start input relay for output", "err", err)
		return err
	}

	// Build ffmpeg args for output relay
	const defaultFFmpegLoglevel = "info"
	loglevel := rm.ffmpegLogLevel
	if loglevel == "" {
		loglevel = defaultFFmpegLoglevel
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

	// Resolve outputURL for FFmpeg (strip file:// prefix and make it relative to recDir)
	resolvedOutputURL := outputURL
	if strings.HasPrefix(outputURL, "file://") {
		// Strip file:// prefix and resolve relative to recDir
		relativePath := strings.TrimPrefix(outputURL, "file://")
		resolvedOutputURL = filepath.Join(rm.recDir, relativePath)
		rm.Logger.Debug("Resolved file output URL", "original", outputURL, "resolved", resolvedOutputURL)
	}

	args = append(args, "-f", "flv", resolvedOutputURL)

	// Convert FFmpegOptions to map for storage
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
		Timeout:        rm.outputTimeout,
		PlatformPreset: preset,
		FFmpegOptions:  optsMap,
		FFmpegArgs:     args,
	}
	err = rm.OutputRelays.StartOutputRelay(config)
	if err != nil {
		rm.Logger.Error("Failed to start output relay", "err", err)
		rm.InputRelays.ReleaseStream(inputName)
		return err
	}

	rm.Logger.Info("Started relay", "inputName", inputName, "inputURL", inputURL, "outputName", outputName, "outputURL", outputURL)
	return nil
}

// StopRelay stops a relay endpoint for an input/output URL
func (rm *RelayManager) StopRelay(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("StopRelay called", "inputURL", inputURL, "outputURL", outputURL, "inputName", inputName, "outputName", outputName)

	// Stop the output relay first
	rm.OutputRelays.StopOutputRelay(outputURL)

	// Decrement the input relay reference count (RTSP cleanup is handled internally)
	rm.InputRelays.StopInputRelay(inputURL)

	return nil
}

// DeleteInput deletes an entire input relay and all its associated outputs
func (rm *RelayManager) DeleteInput(inputURL, inputName string) error {
	rm.Logger.Debug("DeleteInput called", "inputURL", inputURL, "inputName", inputName)

	// First, find and delete all output relays associated with this input
	rm.OutputRelays.mu.Lock()
	var outputsToDelete []string
	for outputURL, relay := range rm.OutputRelays.Relays {
		if relay.InputURL == inputURL {
			outputsToDelete = append(outputsToDelete, outputURL)
		}
	}
	rm.OutputRelays.mu.Unlock()

	// Delete all associated outputs
	for _, outputURL := range outputsToDelete {
		err := rm.OutputRelays.DeleteOutput(outputURL)
		if err != nil {
			rm.Logger.Error("Failed to delete output relay", "outputURL", outputURL, "err", err)
		}
	}

	// Stop any active recordings for this input
	if rm.RecordingManager != nil {
		err := rm.RecordingManager.StopRecording(inputName, inputURL)
		if err != nil {
			rm.Logger.Warn("Failed to stop recording for input", "inputName", inputName, "err", err)
		}
	}

	// Delete the input relay
	err := rm.InputRelays.DeleteInput(inputURL)
	if err != nil {
		rm.Logger.Error("Failed to delete input relay", "inputURL", inputURL, "err", err)
		return err
	}

	// Delete HLS session for this input
	if rm.HLSManager != nil {
		rm.HLSManager.DeleteSession(inputName)
	}

	rm.Logger.Info("Deleted input relay and all associated outputs", "inputName", inputName, "inputURL", inputURL)
	return nil
}

// DeleteOutput deletes a single output relay
func (rm *RelayManager) DeleteOutput(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("DeleteOutput called", "inputURL", inputURL, "outputURL", outputURL, "inputName", inputName, "outputName", outputName)

	// Ensure the output relay is stopped before deletion to decrement refcount
	rm.OutputRelays.mu.Lock()
	outputRelay, exists := rm.OutputRelays.Relays[outputURL]
	rm.OutputRelays.mu.Unlock()
	if exists {
		outputRelay.mu.Lock()
		isRunning := outputRelay.Status == OutputRunning || outputRelay.Status == OutputStarting
		outputRelay.mu.Unlock()
		if isRunning {
			rm.Logger.Info("DeleteOutput: output relay is running, stopping first", "outputURL", outputURL)
			rm.OutputRelays.StopOutputRelay(outputURL)
			// Also decrement input relay refcount since we are stopping a running output
			rm.InputRelays.StopInputRelay(inputURL)
		}
	}

	// Delete the output relay (this will also clean up input relay refcount via callback if not already done)
	err := rm.OutputRelays.DeleteOutput(outputURL)
	if err != nil {
		rm.Logger.Error("Failed to delete output relay", "outputURL", outputURL, "err", err)
		return err
	}
	rm.Logger.Info("Output relay deleted", "inputURL", inputURL, "outputURL", outputURL, "action", "DeleteOutput")
	// Log input relay refcount after output deletion
	rm.InputRelays.mu.Lock()
	if inputRelay, ok := rm.InputRelays.Relays[inputURL]; ok {
		inputRelay.mu.Lock()
		rm.Logger.Info("Input relay refcount after output deletion", "inputURL", inputURL, "outputURL", outputURL, "refCount", inputRelay.RefCount, "action", "DeleteOutput")
		inputRelay.mu.Unlock()
	}
	rm.InputRelays.mu.Unlock()

	rm.Logger.Info("Deleted output relay", "inputName", inputName, "inputURL", inputURL, "outputName", outputName, "outputURL", outputURL)
	return nil
}

// ExportConfig saves the current relay configurations to a file (now includes names and presets)
func (rm *RelayManager) ExportConfig(filename string) error {
	rm.Logger.Debug("ExportConfig called", "filename", filename)

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

	// Get running input relays safely
	runningInputs := rm.InputRelays.GetRunningInputRelays()

	for _, in := range runningInputs {
		var outputs []exportOutput

		// Get outputs for this input
		// We need to access OutputRelays safely
		// Since OutputRelays map is protected by its own mutex, we can iterate it
		// But we need to filter by inputURL.
		// For now, let's assume we can iterate OutputRelays.Relays safely if we lock it.
		// Or we can add a method to OutputRelays to get outputs for an input.

		// Let's iterate OutputRelays directly for now, assuming we can lock it.
		// Ideally OutputRelayManager should provide this.
		rm.OutputRelays.mu.Lock()
		for outURL, out := range rm.OutputRelays.Relays {
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
		rm.OutputRelays.mu.Unlock()

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

// ImportConfig loads relay configurations from a file (now supports names)
func (rm *RelayManager) ImportConfig(filename string) error {
	rm.Logger.Debug("ImportConfig called", "filename", filename)
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
		rm.Logger.Error("Failed to read file", "filename", filename, "err", err)
		return err
	}
	var configs []importConfig
	err = json.Unmarshal(data, &configs)
	if err != nil {
		rm.Logger.Error("Failed to unmarshal config", "err", err)
		return err
	}

	// Start all relays in parallel for faster startup
	var wg sync.WaitGroup
	errorChan := make(chan error, 100) // Buffer for potential errors

	// Register all input configurations first
	for _, relayCfg := range configs {
		rm.InputRelays.RegisterInputConfig(relayCfg.InputName, relayCfg.InputURL)
	}

	for _, relayCfg := range configs {
		for _, out := range relayCfg.Outputs {
			wg.Add(1)
			go func(inputURL, inputName, outputURL, outputName, preset string, ffmpegOpts map[string]string) {
				defer wg.Done()

				// Apply preset and options using centralized helper (no stored config for imports)
				opts, _ := rm.applyPresetAndOptions(preset, ffmpegOpts, "", "")

				err := rm.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, opts, preset)
				if err != nil {
					rm.Logger.Error("Failed to start relay", "inputName", inputName, "outputName", outputName, "err", err)
					select {
					case errorChan <- err:
					default: // Don't block if channel is full
					}
				}
			}(relayCfg.InputURL, relayCfg.InputName, out.OutputURL, out.OutputName, out.PlatformPreset, out.FFmpegOptions)
		}
	}

	// Wait for all relays to start
	wg.Wait()
	close(errorChan)

	// Check if there were any errors
	var lastErr error
	errorCount := 0
	for err := range errorChan {
		rm.Logger.Error("Relay start error during import", "err", err)
		lastErr = err
		errorCount++
	}

	if errorCount > 0 {
		rm.Logger.Error("Import completed with errors", "errorCount", errorCount, "lastError", lastErr)
	} else {
		rm.Logger.Info("Imported relay config successfully", "filename", filename)
	}
	return lastErr
}

// GetEndpointConfig retrieves the stored platform preset and ffmpeg options for an existing output relay
func (rm *RelayManager) GetEndpointConfig(inputURL, outputURL string) (string, *FFmpegOptions, error) {
	rm.OutputRelays.mu.Lock()
	out, exists := rm.OutputRelays.Relays[outputURL]
	rm.OutputRelays.mu.Unlock()
	if !exists || out.InputURL != inputURL {
		return "", nil, fmt.Errorf("no output relay for input %s and output %s", inputURL, outputURL)
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

// RelayStatusV2 includes both input and output relay statuses for UI
// (for responsive, accessible frontend columns)
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

// ServerStatus represents server resource usage
type ServerStatus struct {
	CPU float64 `json:"cpu"`
	Mem uint64  `json:"mem"`
}

// StatusV2Response is the new status API response with server and relay stats
// Used for both backend and frontend
type StatusV2Response struct {
	Server ServerStatus    `json:"server"`
	Relays []RelayStatusV2 `json:"relays"`
}

// StatusV2 returns a struct with server stats and relay statuses for UI
func (rm *RelayManager) StatusV2() StatusV2Response {
	// Return cached snapshot if fresh
	rm.statusCacheMu.Lock()
	if !rm.statusCacheTs.IsZero() && time.Since(rm.statusCacheTs) < statusCacheTTL {
		snapshot := rm.statusCache
		rm.statusCacheMu.Unlock()
		return snapshot
	}
	rm.statusCacheMu.Unlock()

	srv, _ := process.GetSelfUsage()
	serverStatus := ServerStatus{}
	if srv != nil {
		serverStatus = ServerStatus{CPU: srv.CPU, Mem: srv.Mem}
	}
	statuses := make([]RelayStatusV2, 0)

	// Copy maps under their locks to avoid holding multiple locks simultaneously
	rm.InputRelays.mu.Lock()
	inputs := make([]*InputRelay, 0, len(rm.InputRelays.Relays))
	for _, in := range rm.InputRelays.Relays {
		inputs = append(inputs, in)
	}
	rm.InputRelays.mu.Unlock()

	rm.OutputRelays.mu.Lock()
	outputsAll := make([]*OutputRelay, 0, len(rm.OutputRelays.Relays))
	for _, out := range rm.OutputRelays.Relays {
		outputsAll = append(outputsAll, out)
	}
	rm.OutputRelays.mu.Unlock()

	for _, in := range inputs {
		in.mu.Lock()
		cpu, mem := 0.0, uint64(0)
		if in.Proc != nil {
			pid := in.Proc.GetPID()
			if pid > 0 {
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
			speed, _ := in.Proc.GetSpeed()
			inputStatus.Speed = speed
			rm.Logger.Debug("StatusV2: Input relay speed", "inputURL", in.InputURL, "speed", speed)
		}

		// Collect outputs matching this inputURL from the copied slice (no global locks held)
		outputs := make([]OutputRelayStatusV2, 0)
		for _, out := range outputsAll {
			// Quick check without locks on immutable fields
			if out.InputURL != in.InputURL {
				continue
			}
			out.mu.Lock()
			cpuO, memO := 0.0, uint64(0)
			if out.Proc != nil {
				pid := out.Proc.GetPID()
				if pid > 0 {
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
					rm.Logger.Debug("StatusV2: Output relay bitrate", "outputURL", out.OutputURL, "bitrate", bitrate)
				}
			}
			outputs = append(outputs, outputStatus)
			out.mu.Unlock()
		}

		statuses = append(statuses, RelayStatusV2{
			Input:   inputStatus,
			Outputs: outputs,
		})
		in.mu.Unlock()
	}

	resp := StatusV2Response{
		Server: serverStatus,
		Relays: statuses,
	}

	// store in cache
	rm.statusCacheMu.Lock()
	rm.statusCache = resp
	rm.statusCacheTs = time.Now()
	rm.statusCacheMu.Unlock()

	return resp
}

func inputRelayStatusString(s InputRelayStatus) string {
	switch s {
	case InputStarting:
		return "Starting"
	case InputRunning:
		return "Running"
	case InputError:
		return "Error"
	default:
		return "Stopped"
	}
}

func outputRelayStatusString(s OutputRelayStatus) string {
	switch s {
	case OutputStarting:
		return "Starting"
	case OutputRunning:
		return "Running"
	case OutputError:
		return "Error"
	default:
		return "Stopped"
	}
}

// StopAllRelays stops all active input and output relays gracefully
func (rm *RelayManager) StopAllRelays() {
	rm.Logger.Info("RelayManager: Stopping all active relays...")

	// Stop all output relays first by iterating directly over the map
	// This is more efficient than using StatusV2() during shutdown
	rm.OutputRelays.mu.Lock()
	var outputsToStop []struct {
		inputURL, outputURL, outputName string
	}

	// Collect outputs to stop while holding the lock
	for _, output := range rm.OutputRelays.Relays {
		output.mu.Lock()
		// Only stop relays that are actually running or starting
		if output.Status == OutputRunning || output.Status == OutputStarting {
			outputsToStop = append(outputsToStop, struct {
				inputURL, outputURL, outputName string
			}{
				inputURL:   output.InputURL,
				outputURL:  output.OutputURL,
				outputName: output.OutputName,
			})
		} else {
			rm.Logger.Debug("RelayManager: Skipping output relay", "outputName", output.OutputName, "status", outputRelayStatusString(output.Status))
			// Explicitly set status to OutputStopped for skipped relays
			output.Status = OutputStopped
		}
		output.mu.Unlock()
	}
	rm.OutputRelays.mu.Unlock()

	// Now stop the collected outputs without holding the main lock
	for _, toStop := range outputsToStop {
		// Look up input name for logging
		var inputName string
		rm.InputRelays.mu.Lock()
		if inputRelay, exists := rm.InputRelays.Relays[toStop.inputURL]; exists {
			inputName = inputRelay.InputName
		} else {
			inputName = toStop.inputURL // fallback to URL if name not found
		}
		rm.InputRelays.mu.Unlock()

		rm.Logger.Info("RelayManager: Stopping output relay", "inputName", inputName, "outputName", toStop.outputName)
		if err := rm.StopRelay(toStop.inputURL, toStop.outputURL, inputName, toStop.outputName); err != nil {
			rm.Logger.Error("RelayManager: Failed to stop output relay", "inputName", inputName, "outputName", toStop.outputName, "err", err)
		}
	}

	// Verify that all input relays have been stopped due to reference counting
	// If any are still active, it indicates a bug in the reference counting logic
	rm.InputRelays.mu.Lock()
	activeInputs := 0
	var inputsToForceStop []string
	for inputURL, inputRelay := range rm.InputRelays.Relays {
		inputRelay.mu.Lock()
		if inputRelay.Status == InputRunning || inputRelay.Status == InputStarting {
			activeInputs++
			rm.Logger.Error("RelayManager: Input relay still active after stopping all outputs", "inputName", inputRelay.InputName, "inputURL", inputURL, "refCount", inputRelay.RefCount, "status", inputRelayStatusString(inputRelay.Status))
			inputsToForceStop = append(inputsToForceStop, inputURL)
		}
		inputRelay.mu.Unlock()
	}
	rm.InputRelays.mu.Unlock()

	// Force stop any remaining active input relays
	if len(inputsToForceStop) > 0 {
		rm.Logger.Warn("RelayManager: Force stopping remaining input relays due to refcount issues", "count", len(inputsToForceStop))
		for _, inputURL := range inputsToForceStop {
			rm.Logger.Warn("RelayManager: Force stopping remaining input relay", "inputURL", inputURL)
			rm.InputRelays.ForceStopInputRelay(inputURL)
		}
	}

	if activeInputs > 0 {
		rm.Logger.Error("RelayManager: Found active input relays after stopping all outputs - forced shutdown applied", "activeCount", activeInputs)
	} else {
		rm.Logger.Info("RelayManager: All input relays properly stopped via reference counting")
	}

	// Explicitly set all input relay statuses to InputStopped
	rm.InputRelays.mu.Lock()
	for _, inputRelay := range rm.InputRelays.Relays {
		inputRelay.mu.Lock()
		inputRelay.Status = InputStopped
		inputRelay.mu.Unlock()
	}
	rm.InputRelays.mu.Unlock()

	rm.Logger.Info("RelayManager: All relays stopped")
}

// SetTimeouts configures the input and output relay timeouts
func (rm *RelayManager) SetTimeouts(inputTimeout, outputTimeout time.Duration) {
	rm.InputRelays.SetInputTimeout(inputTimeout)
	rm.outputTimeout = outputTimeout
	rm.Logger.Debug("RelayManager: Updated timeouts", "inputTimeout", inputTimeout, "outputTimeout", outputTimeout)
}

// getStartMutex returns a mutex for the given input URL to serialize concurrent starts
func (rm *RelayManager) getStartMutex(inputURL string) *sync.Mutex {
	rm.startMutexesMu.Lock()
	defer rm.startMutexesMu.Unlock()

	if mutex, exists := rm.startMutexes[inputURL]; exists {
		return mutex
	}

	// Create new mutex for this input URL
	mutex := &sync.Mutex{}
	rm.startMutexes[inputURL] = mutex
	return mutex
}

// NewRelayManagerWithFFmpegLoglevel creates a relay manager with configurable ffmpeg loglevel
// (removed, use NewRelayManager with ffmpegLogLevel argument)

// SetHLSManager sets the HLSManager reference for relay manager
func (rm *RelayManager) SetHLSManager(hlsMgr *HLSManager) {
	rm.HLSManager = hlsMgr
}

// SetRecordingManager sets the RecordingManager reference for relay manager
func (rm *RelayManager) SetRecordingManager(recMgr *RecordingManager) {
	rm.RecordingManager = recMgr
}
