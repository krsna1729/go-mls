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

// RelayOperation represents operations on relays communicated via channels
type RelayOperation struct {
	Type       string // "start", "stop", "delete", "cleanup"
	InputURL   string
	OutputURL  string
	InputName  string
	OutputName string
	Options    *FFmpegOptions // FFmpeg options for start operations
	Preset     string         // Platform preset name for start operations
	Response   chan error     // Channel to send back the result
}

// RelayManager manages all relays (per input URL) using channels for coordination
type RelayManager struct {
	InputRelays  *InputRelayManager
	OutputRelays *OutputRelayManager
	Logger       *logger.Logger
	rtspServer   *RTSPServerManager // RTSP server for local relays
	recDir       string             // Directory for playing recordings from

	// Configurable timeouts
	inputTimeout  time.Duration
	outputTimeout time.Duration

	// Channel-based coordination to prevent deadlocks
	operationChan chan RelayOperation
	shutdownChan  chan struct{}
}

func NewRelayManager(l *logger.Logger, recDir string) *RelayManager {
	irm := NewInputRelayManager(l, recDir)
	orm := NewOutputRelayManager(l)
	rm := &RelayManager{
		InputRelays:   irm,
		OutputRelays:  orm,
		Logger:        l,
		recDir:        recDir,
		inputTimeout:  30 * time.Second, // Default values, can be overridden
		outputTimeout: 60 * time.Second,
		operationChan: make(chan RelayOperation, 100), // Buffered to prevent blocking
		shutdownChan:  make(chan struct{}),
	}

	// Set up failure callback using channel communication instead of direct calls
	orm.SetFailureCallback(func(inputURL, outputURL string) {
		// Use non-blocking send to avoid deadlocks
		select {
		case rm.operationChan <- RelayOperation{
			Type:      "cleanup",
			InputURL:  inputURL,
			OutputURL: outputURL,
			Response:  nil, // No response needed for cleanup
		}:
		default:
			l.Warn("Failed to send cleanup operation for %s -> %s (channel full)", inputURL, outputURL)
		}
	})

	// Start the coordinator goroutine
	go rm.coordinator()

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

	// Send operation to coordinator and wait for response
	respChan := make(chan error, 1)

	// Create start operation with all necessary data
	startOp := RelayOperation{
		Type:       "start",
		InputURL:   inputURL,
		OutputURL:  outputURL,
		InputName:  inputName,
		OutputName: outputName,
		Response:   respChan,
		Options:    opts,
		Preset:     preset,
	}

	select {
	case rm.operationChan <- startOp:
		// Wait for coordinator to process the operation
		return <-respChan
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for start operation to be processed")
	}
}

// StopRelay stops a relay endpoint for an input/output URL
func (rm *RelayManager) StopRelay(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("StopRelay called: input=%s, output=%s, input_name=%s, output_name=%s", inputURL, outputURL, inputName, outputName)

	// Send operation to coordinator and wait for response
	respChan := make(chan error, 1)

	stopOp := RelayOperation{
		Type:       "stop",
		InputURL:   inputURL,
		OutputURL:  outputURL,
		InputName:  inputName,
		OutputName: outputName,
		Response:   respChan,
	}

	select {
	case rm.operationChan <- stopOp:
		// Wait for coordinator to process the operation
		return <-respChan
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for stop operation to be processed")
	}
}

// DeleteInput deletes an entire input relay and all its associated outputs
func (rm *RelayManager) DeleteInput(inputURL, inputName string) error {
	rm.Logger.Debug("DeleteInput called: input=%s, input_name=%s", inputURL, inputName)

	// Send operation to coordinator and wait for response
	respChan := make(chan error, 1)

	deleteOp := RelayOperation{
		Type:      "delete_input",
		InputURL:  inputURL,
		InputName: inputName,
		Response:  respChan,
	}

	select {
	case rm.operationChan <- deleteOp:
		// Wait for coordinator to process the operation
		return <-respChan
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for delete input operation to be processed")
	}
}

// DeleteOutput deletes a single output relay
func (rm *RelayManager) DeleteOutput(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("DeleteOutput called: input=%s, output=%s, input_name=%s, output_name=%s", inputURL, outputURL, inputName, outputName)

	// Send operation to coordinator and wait for response
	respChan := make(chan error, 1)

	deleteOp := RelayOperation{
		Type:       "delete_output",
		InputURL:   inputURL,
		OutputURL:  outputURL,
		InputName:  inputName,
		OutputName: outputName,
		Response:   respChan,
	}

	select {
	case rm.operationChan <- deleteOp:
		// Wait for coordinator to process the operation
		return <-respChan
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for delete output operation to be processed")
	}
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
		if in.Cmd != nil && in.PID > 0 {
			// Use cached PID to avoid race conditions
			pid := in.PID
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
			Speed:     in.Speed, // Now using speed instead of bitrate
		}
		rm.Logger.Debug("StatusV2: Input relay %s speed: %.2fx", in.InputURL, in.Speed)
		// Gather outputs for this input
		outputs := []OutputRelayStatusV2{}
		rm.OutputRelays.mu.Lock()
		for _, out := range rm.OutputRelays.Relays {
			if out.InputURL == in.InputURL {
				out.mu.Lock()
				cpuO, memO := 0.0, uint64(0)
				// Safely access process info to avoid data race
				if out.Cmd != nil && out.PID > 0 {
					// Use cached PID to avoid race conditions
					pid := out.PID
					if usage, err := process.GetProcUsage(pid); err == nil {
						cpuO = usage.CPU
						memO = usage.Mem
					}
				}
				outputs = append(outputs, OutputRelayStatusV2{
					OutputURL:  out.OutputURL,
					OutputName: out.OutputName,
					InputURL:   out.InputURL,
					LocalURL:   out.LocalURL,
					Status:     outputRelayStatusString(out.Status),
					LastError:  out.LastError,
					CPU:        cpuO,
					Mem:        memO,
					Bitrate:    out.Bitrate, // Now using actual tracked bitrate
				})
				rm.Logger.Debug("StatusV2: Output relay %s bitrate: %.2f kbps", out.OutputURL, out.Bitrate)
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

	// Now stop the collected outputs using the coordinator
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

	// Finally, shutdown the coordinator
	rm.Logger.Info("RelayManager: Shutting down coordinator...")
	rm.Shutdown()

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

// coordinator handles all relay operations sequentially to prevent deadlocks
func (rm *RelayManager) coordinator() {
	rm.Logger.Debug("RelayManager coordinator started")
	defer rm.Logger.Debug("RelayManager coordinator stopped")

	for {
		select {
		case <-rm.shutdownChan:
			return
		case op := <-rm.operationChan:
			rm.handleOperation(op)
		}
	}
}

// handleOperation processes a single relay operation
func (rm *RelayManager) handleOperation(op RelayOperation) {
	switch op.Type {
	case "start":
		err := rm.handleStartOperation(op)
		if op.Response != nil {
			op.Response <- err
		}

	case "stop":
		err := rm.handleStopOperation(op)
		if op.Response != nil {
			op.Response <- err
		}

	case "delete_input":
		err := rm.handleDeleteInputOperation(op)
		if op.Response != nil {
			op.Response <- err
		}

	case "delete_output":
		err := rm.handleDeleteOutputOperation(op)
		if op.Response != nil {
			op.Response <- err
		}

	case "cleanup":
		// Handle output relay failure cleanup
		rm.Logger.Debug("Coordinator: cleaning up input relay refcount for inputURL=%s", op.InputURL)
		rm.InputRelays.StopInputRelay(op.InputURL)

	default:
		rm.Logger.Error("Coordinator: unknown operation type: %s", op.Type)
		if op.Response != nil {
			op.Response <- fmt.Errorf("unknown operation type: %s", op.Type)
		}
	}
}

// Shutdown gracefully stops the coordinator
func (rm *RelayManager) Shutdown() {
	close(rm.shutdownChan)
}

// handleStartOperation processes a start relay operation
func (rm *RelayManager) handleStartOperation(op RelayOperation) error {
	// Compose local RTSP relay path and URL
	relayPath := fmt.Sprintf("relay/%s", op.InputName)
	localRelayURL := fmt.Sprintf("%s/%s", GetRTSPServerURL(), relayPath)

	// Start or get the input relay (the InputRelayManager internally handles serialization)
	_, err := rm.InputRelays.StartInputRelay(op.InputName, op.InputURL, localRelayURL, rm.inputTimeout)
	if err != nil {
		rm.Logger.Error("Failed to start input relay for output: %v", err)
		return err
	}

	// Wait for the RTSP stream to become ready before starting output ffmpeg
	if rm.rtspServer != nil {
		rm.Logger.Info("Waiting for RTSP stream to become ready: %s", relayPath)
		err = rm.rtspServer.WaitForStreamReady(relayPath, 30*time.Second)
		if err != nil {
			rm.Logger.Error("Failed to wait for RTSP stream to become ready for %s: %v", op.InputName, err)
			if !rm.rtspServer.IsStreamReady(relayPath) {
				rm.InputRelays.StopInputRelay(op.InputURL)
				return fmt.Errorf("RTSP stream not ready: %v", err)
			}
			rm.Logger.Warn("Stream %s appears ready but wait failed, continuing anyway", relayPath)
		} else {
			rm.Logger.Info("RTSP stream is ready for %s, starting output relay", op.InputName)
		}
	}

	// Build ffmpeg args for output relay
	args := []string{"-hide_banner", "-loglevel", "info", "-stats", "-re", "-i", localRelayURL}
	if op.Options != nil {
		if op.Options.VideoCodec != "" {
			args = append(args, "-c:v", op.Options.VideoCodec)
		}
		if op.Options.AudioCodec != "" {
			args = append(args, "-c:a", op.Options.AudioCodec)
		}
		if op.Options.Resolution != "" {
			args = append(args, "-s", op.Options.Resolution)
		}
		if op.Options.Framerate != "" {
			args = append(args, "-r", op.Options.Framerate)
		}
		if op.Options.Bitrate != "" {
			args = append(args, "-b:v", op.Options.Bitrate)
		}
		if op.Options.Rotation != "" {
			args = append(args, "-vf", op.Options.Rotation)
		}
		if len(op.Options.ExtraArgs) > 0 {
			args = append(args, op.Options.ExtraArgs...)
		}
	}
	args = append(args, "-f", "flv", op.OutputURL)

	// Convert FFmpegOptions to map for storage
	var optsMap map[string]string
	if op.Options != nil {
		optsMap = map[string]string{
			"video_codec": op.Options.VideoCodec,
			"audio_codec": op.Options.AudioCodec,
			"resolution":  op.Options.Resolution,
			"framerate":   op.Options.Framerate,
			"bitrate":     op.Options.Bitrate,
			"rotation":    op.Options.Rotation,
		}
	}

	config := OutputRelayConfig{
		OutputURL:      op.OutputURL,
		OutputName:     op.OutputName,
		InputURL:       op.InputURL,
		LocalURL:       localRelayURL,
		Timeout:        rm.outputTimeout,
		PlatformPreset: op.Preset,
		FFmpegOptions:  optsMap,
		FFmpegArgs:     args,
	}
	err = rm.OutputRelays.StartOutputRelay(config)
	if err != nil {
		rm.Logger.Error("Failed to start output relay: %v", err)
		return err
	}

	rm.Logger.Info("Started relay: %s [%s] -> %s [%s]", op.InputName, op.InputURL, op.OutputName, op.OutputURL)
	return nil
}

// handleStopOperation processes a stop relay operation
func (rm *RelayManager) handleStopOperation(op RelayOperation) error {
	// Stop the output relay first
	rm.OutputRelays.StopOutputRelay(op.OutputURL)

	// Decrement the input relay reference count (RTSP cleanup is handled internally)
	rm.InputRelays.StopInputRelay(op.InputURL)

	return nil
}

// handleDeleteInputOperation processes a delete input operation
func (rm *RelayManager) handleDeleteInputOperation(op RelayOperation) error {
	// First, find and delete all output relays associated with this input
	rm.OutputRelays.mu.Lock()
	var outputsToDelete []string
	for outputURL, relay := range rm.OutputRelays.Relays {
		if relay.InputURL == op.InputURL {
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
	err := rm.InputRelays.DeleteInput(op.InputURL)
	if err != nil {
		rm.Logger.Error("Failed to delete input relay %s: %v", op.InputURL, err)
		return err
	}

	rm.Logger.Info("Deleted input relay and all associated outputs: %s [%s]", op.InputName, op.InputURL)
	return nil
}

// handleDeleteOutputOperation processes a delete output operation
func (rm *RelayManager) handleDeleteOutputOperation(op RelayOperation) error {
	// Delete the output relay (this will also clean up input relay refcount via callback)
	err := rm.OutputRelays.DeleteOutput(op.OutputURL)
	if err != nil {
		rm.Logger.Error("Failed to delete output relay %s: %v", op.OutputURL, err)
		return err
	}

	rm.Logger.Info("Deleted output relay: %s [%s] -> %s [%s]", op.InputName, op.InputURL, op.OutputName, op.OutputURL)
	return nil
}
