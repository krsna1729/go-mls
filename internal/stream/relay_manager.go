package stream

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/process"
)

// InputConfig stores persistent input configuration
type InputConfig struct {
	InputURL  string `json:"input_url"`
	InputName string `json:"input_name"`
}

// RelayManager manages all relays (per input URL)
type RelayManager struct {
	InputRelays  *InputRelayManager
	OutputRelays *OutputRelayManager
	Logger       *logger.Logger
	rtspServer   *RTSPServerManager // RTSP server for local relays
	recDir       string             // Directory for playing recordings from

	// Configuration registry for persistent input mappings
	inputConfigs map[string]*InputConfig // inputName -> InputConfig
	configMu     sync.RWMutex            // Protects inputConfigs

	// Configurable timeouts
	inputTimeout  time.Duration
	outputTimeout time.Duration

	// Mutex map for serializing concurrent starts of the same input URL
	startMutexes   map[string]*sync.Mutex
	startMutexesMu sync.Mutex
}

func NewRelayManager(l *logger.Logger, recDir string) *RelayManager {
	irm := NewInputRelayManager(l, recDir)
	orm := NewOutputRelayManager(l)
	rm := &RelayManager{
		InputRelays:   irm,
		OutputRelays:  orm,
		Logger:        l,
		recDir:        recDir,
		inputConfigs:  make(map[string]*InputConfig),
		inputTimeout:  30 * time.Second, // Default values, can be overridden
		outputTimeout: 60 * time.Second,
		startMutexes:  make(map[string]*sync.Mutex),
	}

	// Set up failure callback for output relays to clean up input relay refcount
	orm.SetFailureCallback(func(inputURL, outputURL string) {
		l.Debug("Output relay failure callback: cleaning up input relay refcount for inputURL=%s", inputURL)
		irm.StopInputRelay(inputURL) // RTSP cleanup is handled internally
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
	rm.Logger.Debug("StartRelayWithOptions called: input=%s, output=%s, input_name=%s, output_name=%s, preset=%s", inputURL, outputURL, inputName, outputName, preset)

	// Register input configuration for future HLS access
	rm.RegisterInputConfig(inputName, inputURL)

	// Get mutex for this input URL to serialize concurrent starts
	startMutex := rm.getStartMutex(inputURL)
	startMutex.Lock()
	defer startMutex.Unlock()

	// Compose local RTSP relay path and URL
	relayPath := fmt.Sprintf("relay/%s", inputName)
	localRelayURL := fmt.Sprintf("%s/%s", GetRTSPServerURL(), relayPath)

	// Start or get the input relay
	_, err := rm.InputRelays.StartInputRelay(inputName, inputURL, localRelayURL, rm.inputTimeout)
	if err != nil {
		rm.Logger.Error("Failed to start input relay for output: %v", err)
		return err
	}

	// Wait for the RTSP stream to become ready before starting output ffmpeg
	if rm.rtspServer != nil {
		rm.Logger.Info("Waiting for RTSP stream to become ready: %s", relayPath)
		err = rm.rtspServer.WaitForStreamReady(relayPath, 30*time.Second)
		if err != nil {
			rm.Logger.Error("Failed to wait for RTSP stream to become ready for %s: %v", inputName, err)
			if !rm.rtspServer.IsStreamReady(relayPath) {
				rm.InputRelays.StopInputRelay(inputURL)
				return fmt.Errorf("RTSP stream not ready: %v", err)
			}
			rm.Logger.Warn("Stream %s appears ready but wait failed, continuing anyway", relayPath)
		} else {
			rm.Logger.Info("RTSP stream is ready for %s, starting output relay", inputName)
		}
	}

	// Build ffmpeg args for output relay
	args := []string{"-hide_banner", "-loglevel", "info", "-stats", "-re", "-i", localRelayURL}
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
	args = append(args, "-f", "flv", outputURL)

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
		rm.Logger.Error("Failed to start output relay: %v", err)
		return err
	}

	rm.Logger.Info("Started relay: %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
	return nil
}

// StopRelay stops a relay endpoint for an input/output URL
func (rm *RelayManager) StopRelay(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("StopRelay called: input=%s, output=%s, input_name=%s, output_name=%s", inputURL, outputURL, inputName, outputName)

	// Stop the output relay first
	rm.OutputRelays.StopOutputRelay(outputURL)

	// Decrement the input relay reference count (RTSP cleanup is handled internally)
	rm.InputRelays.StopInputRelay(inputURL)

	return nil
}

// DeleteInput deletes an entire input relay and all its associated outputs
func (rm *RelayManager) DeleteInput(inputURL, inputName string) error {
	rm.Logger.Debug("DeleteInput called: input=%s, input_name=%s", inputURL, inputName)

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
			rm.Logger.Error("Failed to delete output relay %s: %v", outputURL, err)
		}
	}

	// Delete the input relay
	err := rm.InputRelays.DeleteInput(inputURL)
	if err != nil {
		rm.Logger.Error("Failed to delete input relay %s: %v", inputURL, err)
		return err
	}

	rm.Logger.Info("Deleted input relay and all associated outputs: %s [%s]", inputName, inputURL)
	return nil
}

// DeleteOutput deletes a single output relay
func (rm *RelayManager) DeleteOutput(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("DeleteOutput called: input=%s, output=%s, input_name=%s, output_name=%s", inputURL, outputURL, inputName, outputName)

	// Delete the output relay (this will also clean up input relay refcount via callback)
	err := rm.OutputRelays.DeleteOutput(outputURL)
	if err != nil {
		rm.Logger.Error("Failed to delete output relay %s: %v", outputURL, err)
		return err
	}

	rm.Logger.Info("Deleted output relay: %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
	return nil
}

// ExportConfig saves the current relay configurations to a file (now includes names and presets)
func (rm *RelayManager) ExportConfig(filename string) error {
	rm.Logger.Debug("ExportConfig called: filename=%s", filename)
	type exportConfig struct {
		InputURL  string `json:"input_url"`
		InputName string `json:"input_name"`
		Outputs   []struct {
			OutputURL      string            `json:"output_url"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset,omitempty"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
		} `json:"outputs"`
	}
	var configs []exportConfig
	rm.InputRelays.mu.Lock()
	for _, in := range rm.InputRelays.Relays {
		in.mu.Lock()
		var outputs []struct {
			OutputURL      string            `json:"output_url"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset,omitempty"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
		}
		rm.OutputRelays.mu.Lock()
		for _, out := range rm.OutputRelays.Relays {
			if out.InputURL == in.InputURL {
				outputs = append(outputs, struct {
					OutputURL      string            `json:"output_url"`
					OutputName     string            `json:"output_name"`
					PlatformPreset string            `json:"platform_preset,omitempty"`
					FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
				}{
					OutputURL:      out.OutputURL,
					OutputName:     out.OutputName,
					PlatformPreset: out.PlatformPreset,
					FFmpegOptions:  out.FFmpegOptions,
				})
			}
		}
		rm.OutputRelays.mu.Unlock()
		configs = append(configs, exportConfig{
			InputURL:  in.InputURL,
			InputName: in.InputName,
			Outputs:   outputs,
		})
		in.mu.Unlock()
	}
	rm.InputRelays.mu.Unlock()
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// ImportConfig loads relay configurations from a file (now supports names)
func (rm *RelayManager) ImportConfig(filename string) error {
	rm.Logger.Debug("ImportConfig called: filename=%s", filename)
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
		rm.Logger.Error("Failed to read file %s: %v", filename, err)
		return err
	}
	var configs []importConfig
	err = json.Unmarshal(data, &configs)
	if err != nil {
		rm.Logger.Error("Failed to unmarshal config: %v", err)
		return err
	}

	// Start all relays in parallel for faster startup
	var wg sync.WaitGroup
	errorChan := make(chan error, 100) // Buffer for potential errors

	// Register all input configurations first
	for _, relayCfg := range configs {
		rm.RegisterInputConfig(relayCfg.InputName, relayCfg.InputURL)
	}

	for _, relayCfg := range configs {
		for _, out := range relayCfg.Outputs {
			wg.Add(1)
			go func(inputURL, inputName, outputURL, outputName, preset string, ffmpegOpts map[string]string) {
				defer wg.Done()

				var opts *FFmpegOptions
				if ffmpegOpts != nil {
					opts = &FFmpegOptions{
						VideoCodec: ffmpegOpts["video_codec"],
						AudioCodec: ffmpegOpts["audio_codec"],
						Resolution: ffmpegOpts["resolution"],
						Framerate:  ffmpegOpts["framerate"],
						Bitrate:    ffmpegOpts["bitrate"],
						Rotation:   ffmpegOpts["rotation"],
					}
				}

				err := rm.StartRelayWithOptions(inputURL, outputURL, inputName, outputName, opts, preset)
				if err != nil {
					rm.Logger.Error("Failed to start relay %s -> %s: %v", inputName, outputName, err)
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
		rm.Logger.Error("Relay start error during import: %v", err)
		lastErr = err
		errorCount++
	}

	if errorCount > 0 {
		rm.Logger.Error("Import completed with %d errors, last error: %v", errorCount, lastErr)
	} else {
		rm.Logger.Info("Imported relay config from %s successfully", filename)
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
	srv, _ := process.GetSelfUsage()
	serverStatus := ServerStatus{}
	if srv != nil {
		serverStatus = ServerStatus{CPU: srv.CPU, Mem: srv.Mem}
	}
	statuses := []RelayStatusV2{}
	// Gather input relays
	rm.InputRelays.mu.Lock()
	for _, in := range rm.InputRelays.Relays {
		in.mu.Lock()
		cpu, mem := 0.0, uint64(0)
		// Safely access process info to avoid data race
		if in.Proc != nil && in.Proc.Cmd != nil && in.Proc.Cmd.Process != nil {
			pid := in.Proc.PID
			if usage, err := process.GetProcUsage(pid); err == nil {
				cpu = usage.CPU
				mem = usage.Mem
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
			rm.Logger.Debug("StatusV2: Input relay %s speed: %.2fx", in.InputURL, speed)
		}
		// Gather outputs for this input
		outputs := []OutputRelayStatusV2{}
		rm.OutputRelays.mu.Lock()
		for _, out := range rm.OutputRelays.Relays {
			if out.InputURL == in.InputURL {
				out.mu.Lock()
				cpuO, memO := 0.0, uint64(0)
				// Safely access process info to avoid data race
				if out.Proc != nil && out.Proc.Cmd != nil && out.Proc.Cmd.Process != nil {
					pid := out.Proc.PID
					if usage, err := process.GetProcUsage(pid); err == nil {
						cpuO = usage.CPU
						memO = usage.Mem
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
					bitrate, _ := out.Proc.GetBitrate()
					outputStatus.Bitrate = bitrate
					rm.Logger.Debug("StatusV2: Output relay %s bitrate: %.2f kbps", out.OutputURL, bitrate)
				}
				outputs = append(outputs, outputStatus)
				out.mu.Unlock()
			}
		}
		rm.OutputRelays.mu.Unlock()
		statuses = append(statuses, RelayStatusV2{
			Input:   inputStatus,
			Outputs: outputs,
		})
		in.mu.Unlock()
	}
	rm.InputRelays.mu.Unlock()
	return StatusV2Response{
		Server: serverStatus,
		Relays: statuses,
	}
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
			rm.Logger.Debug("RelayManager: Skipping output relay %s (status: %s)",
				output.OutputName, outputRelayStatusString(output.Status))
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

		rm.Logger.Info("RelayManager: Stopping output relay %s -> %s", inputName, toStop.outputName)
		if err := rm.StopRelay(toStop.inputURL, toStop.outputURL, inputName, toStop.outputName); err != nil {
			rm.Logger.Error("RelayManager: Failed to stop output relay %s -> %s: %v", inputName, toStop.outputName, err)
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
			rm.Logger.Error("RelayManager: Input relay %s [%s] is still active after stopping all outputs (refcount: %d, status: %s)",
				inputRelay.InputName, inputURL, inputRelay.RefCount, inputRelayStatusString(inputRelay.Status))
			inputsToForceStop = append(inputsToForceStop, inputURL)
		}
		inputRelay.mu.Unlock()
	}
	rm.InputRelays.mu.Unlock()

	// Force stop any remaining active input relays
	if len(inputsToForceStop) > 0 {
		rm.Logger.Warn("RelayManager: Force stopping %d remaining input relays due to refcount issues", len(inputsToForceStop))
		for _, inputURL := range inputsToForceStop {
			rm.Logger.Warn("RelayManager: Force stopping remaining input relay %s", inputURL)
			rm.InputRelays.ForceStopInputRelay(inputURL)
		}
	}

	if activeInputs > 0 {
		rm.Logger.Error("RelayManager: Found %d input relays still active after stopping all outputs - forced shutdown applied", activeInputs)
	} else {
		rm.Logger.Info("RelayManager: All input relays properly stopped via reference counting")
	}

	rm.Logger.Info("RelayManager: All relays stopped")
}

// SetTimeouts configures the input and output relay timeouts
func (rm *RelayManager) SetTimeouts(inputTimeout, outputTimeout time.Duration) {
	rm.inputTimeout = inputTimeout
	rm.outputTimeout = outputTimeout
	rm.Logger.Debug("RelayManager: Updated timeouts - input: %v, output: %v", inputTimeout, outputTimeout)
}

// GetInputTimeout returns the configured input timeout
func (rm *RelayManager) GetInputTimeout() time.Duration {
	return rm.inputTimeout
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

// RegisterInputConfig stores an input configuration for later HLS access
func (rm *RelayManager) RegisterInputConfig(inputName, inputURL string) {
	rm.configMu.Lock()
	defer rm.configMu.Unlock()

	rm.inputConfigs[inputName] = &InputConfig{
		InputURL:  inputURL,
		InputName: inputName,
	}
	rm.Logger.Debug("Registered input config: %s -> %s", inputName, inputURL)
}

// GetInputURLByName returns the input URL for a given input name
func (rm *RelayManager) GetInputURLByName(inputName string) (string, bool) {
	// First check if there's a running input relay
	if _, ok := rm.InputRelays.FindLocalURLByInputName(inputName); ok {
		// Find the input URL from the running relay
		rm.InputRelays.mu.Lock()
		defer rm.InputRelays.mu.Unlock()
		for inputURL, relay := range rm.InputRelays.Relays {
			if relay.InputName == inputName {
				return inputURL, true
			}
		}
	}

	// Check stored configuration
	rm.configMu.RLock()
	defer rm.configMu.RUnlock()

	if config, exists := rm.inputConfigs[inputName]; exists {
		return config.InputURL, true
	}

	return "", false
}

// StartInputRelayForConsumer starts an input relay and marks it as having a consumer
// This is used by HLS sessions, recordings, etc. to ensure proper lifecycle management
func (rm *RelayManager) StartInputRelayForConsumer(inputName string) (string, error) {
	inputURL, exists := rm.GetInputURLByName(inputName)
	if !exists {
		return "", fmt.Errorf("input configuration not found for: %s", inputName)
	}

	// Compose local RTSP relay path and URL
	relayPath := fmt.Sprintf("relay/%s", inputName)
	localRelayURL := fmt.Sprintf("%s/%s", GetRTSPServerURL(), relayPath)

	// Start the input relay with consumer counting
	localURL, err := rm.InputRelays.StartInputRelay(inputName, inputURL, localRelayURL, rm.inputTimeout)
	if err != nil {
		return "", fmt.Errorf("failed to start input relay for %s: %v", inputName, err)
	}

	// Wait for the RTSP stream to become ready
	if rm.rtspServer != nil {
		rm.Logger.Info("Waiting for RTSP stream to become ready: %s", relayPath)
		err = rm.rtspServer.WaitForStreamReady(relayPath, 30*time.Second)
		if err != nil {
			rm.Logger.Error("Failed to wait for RTSP stream to become ready for %s: %v", inputName, err)
			if !rm.rtspServer.IsStreamReady(relayPath) {
				rm.InputRelays.StopInputRelay(inputURL)
				return "", fmt.Errorf("RTSP stream not ready: %v", err)
			}
			rm.Logger.Warn("Stream %s appears ready but wait failed, continuing anyway", relayPath)
		}
	}

	return localURL, nil
}

// StopInputRelayForConsumer decrements the consumer count for an input relay
// This is used by HLS sessions, recordings, etc. when they stop consuming
func (rm *RelayManager) StopInputRelayForConsumer(inputName string) {
	inputURL, exists := rm.GetInputURLByName(inputName)
	if !exists {
		rm.Logger.Warn("Cannot stop input relay for %s: input configuration not found", inputName)
		return
	}

	rm.InputRelays.StopInputRelay(inputURL)
}
