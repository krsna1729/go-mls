package stream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/process"
	"go-mls/internal/status"
)

// RelayEndpoint manages a single output URL and its ffmpeg process
type Status int

const (
	Starting Status = iota
	Running
	Stopped
	Error
)

type RelayEndpoint struct {
	OutputURL      string
	OutputName     string
	Cmd            *exec.Cmd
	Status         Status
	Bitrate        float64        // in kbits/s
	PlatformPreset string         // Store the preset name for persistence
	FFmpegOptions  *FFmpegOptions // Store custom options for persistence
	mu             sync.Mutex
}

// Relay manages all endpoints for a single input URL
// Now also manages a single input relay process and refcount for outputs+recordings
type Relay struct {
	InputURL      string
	InputName     string
	Endpoints     map[string]*RelayEndpoint // key: output URL
	LocalRelayURL string                    // e.g. GetRTSPServerURL()/relay/<inputName>
	LocalRelayCmd *exec.Cmd                 // ffmpeg process for input relay
	RefCount      int                       // number of outputs+recordings using this input
	Status        Status                    // status of input relay
	mu            sync.Mutex
}

// RelayManager manages all relays (per input URL)
type RelayManager struct {
	Relays     map[string]*Relay // key: input URL
	mu         sync.Mutex
	Logger     *logger.Logger
	rtspServer *RTSPServerManager // RTSP server for local relays
}

func NewRelayManager(l *logger.Logger) *RelayManager {
	return &RelayManager{
		Relays: make(map[string]*Relay),
		Logger: l,
	}
}

// SetRTSPServer sets the RTSP server instance
func (rm *RelayManager) SetRTSPServer(server *RTSPServerManager) {
	rm.rtspServer = server
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

// buildFFmpegCmdWithOptions builds an ffmpeg command with advanced options and/or preset
func buildFFmpegCmdWithOptions(inputURL, outputURL string, opts *FFmpegOptions, presetName string) *exec.Cmd {
	args := []string{
		"-hide_banner",       // Reduce verbose output
		"-loglevel", "error", // Only show actual errors, suppress H.264 decoder warnings
		"-re", "-i", inputURL, "-progress", "pipe:1",
	}
	var o FFmpegOptions

	// Apply preset if specified
	if preset, ok := PlatformPresets[presetName]; ok {
		o = preset.Options
	} else if presetName == "" && opts == nil {
		// If no preset and no options provided, use copy mode (no transcoding)
		o = FFmpegOptions{
			VideoCodec: "copy",
			AudioCodec: "copy",
		}
	}

	if opts != nil {
		// opts override preset
		if opts.VideoCodec != "" {
			o.VideoCodec = opts.VideoCodec
		}
		if opts.AudioCodec != "" {
			o.AudioCodec = opts.AudioCodec
		}
		if opts.Resolution != "" {
			o.Resolution = opts.Resolution
		}
		if opts.Framerate != "" {
			o.Framerate = opts.Framerate
		}
		if opts.Bitrate != "" {
			o.Bitrate = opts.Bitrate
		}
		if opts.Rotation != "" {
			o.Rotation = opts.Rotation
		}
		if len(opts.ExtraArgs) > 0 {
			o.ExtraArgs = opts.ExtraArgs
		}
	}
	if o.VideoCodec != "" {
		args = append(args, "-c:v", o.VideoCodec)
	}
	if o.AudioCodec != "" {
		args = append(args, "-c:a", o.AudioCodec)
	}
	if o.Resolution != "" {
		args = append(args, "-s", o.Resolution)
	}

	// Build video filter chain for better quality
	var videoFilters []string
	if o.Rotation != "" {
		videoFilters = append(videoFilters, o.Rotation)
	}
	if o.Framerate != "" {
		// Use fps filter for better frame rate conversion instead of just -r
		videoFilters = append(videoFilters, "fps="+o.Framerate)
		args = append(args, "-fps_mode", "cfr")
	}

	// Apply video filters if any
	if len(videoFilters) > 0 {
		filterString := ""
		for i, filter := range videoFilters {
			if i > 0 {
				filterString += ","
			}
			filterString += filter
		}
		args = append(args, "-vf", filterString)
	} else if o.Framerate != "" {
		// If only framerate without other filters, use -r
		args = append(args, "-r", o.Framerate)
		args = append(args, "-fps_mode", "cfr")
	}

	if o.Bitrate != "" {
		args = append(args, "-b:v", o.Bitrate)
	}
	if len(o.ExtraArgs) > 0 {
		args = append(args, o.ExtraArgs...)
	}
	args = append(args, "-f", "flv", outputURL)
	return exec.Command("ffmpeg", args...)
}

// Helper to get a platform preset by name
func GetPlatformPreset(name string) (PlatformPreset, bool) {
	preset, ok := PlatformPresets[name]
	return preset, ok
}

// StartRelay starts a relay for an input/output URL and stores names
// StartRelayWithOptions starts a relay with advanced ffmpeg options and/or platform preset
func (rm *RelayManager) StartRelayWithOptions(inputURL, outputURL, inputName, outputName string, opts *FFmpegOptions, preset string) error {
	rm.Logger.Debug("StartRelayWithOptions called: input=%s, output=%s, input_name=%s, output_name=%s, preset=%s", inputURL, outputURL, inputName, outputName, preset)

	// Get or start the local relay for this input
	localRelayURL, err := rm.StartInputRelay(inputName, inputURL)
	if err != nil {
		rm.Logger.Error("Failed to start input relay for output: %v", err)
		return err
	}

	// Wait for the RTSP stream to become ready before starting output ffmpeg
	if rm.rtspServer != nil {
		relayPath := fmt.Sprintf("relay/%s", inputName)
		rm.Logger.Info("Waiting for RTSP stream to become ready: %s", relayPath)
		// Use 30 second timeout to allow for network delays and connection establishment
		err = rm.rtspServer.WaitForStreamReady(relayPath, 30*time.Second)
		if err != nil {
			rm.Logger.Error("Failed to wait for RTSP stream to become ready for %s: %v", inputName, err)
			rm.Logger.Debug("Stream readiness check failed for %s, checking if stream exists...", relayPath)
			if rm.rtspServer.IsStreamReady(relayPath) {
				rm.Logger.Warn("Stream %s appears ready but wait failed, continuing anyway", relayPath)
			} else {
				rm.StopInputRelay(inputName, inputURL)
				return fmt.Errorf("RTSP stream not ready: %v", err)
			}
		} else {
			rm.Logger.Info("RTSP stream is ready for %s, starting output relay", inputName)
		}
	}
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	if !exists {
		relay = &Relay{
			InputURL:  inputURL,
			InputName: inputName,
			Endpoints: make(map[string]*RelayEndpoint),
		}
		rm.Relays[inputURL] = relay
	} else if relay.InputName == "" {
		relay.InputName = inputName
	}
	rm.mu.Unlock()

	relay.mu.Lock()
	if ep, exists := relay.Endpoints[outputURL]; exists && ep.Status == Running {
		rm.Logger.Warn("Relay already running for %s [%s] -> %s [%s]", relay.InputName, inputURL, ep.OutputName, outputURL)
		relay.mu.Unlock()
		return fmt.Errorf("relay already running for %s -> %s", inputURL, outputURL)
	}
	// Use local relay URL as input, the RTSP stream is already confirmed ready
	cmd := buildFFmpegCmdWithOptions(localRelayURL, outputURL, opts, preset)
	rm.Logger.Debug("Starting ffmpeg process for output: %v", cmd.Args)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		rm.Logger.Error("Failed to create stdout pipe for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		// Decrement input relay refcount on failure
		rm.StopInputRelay(inputName, inputURL)
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rm.Logger.Error("Failed to create stderr pipe for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		// Decrement input relay refcount on failure
		rm.StopInputRelay(inputName, inputURL)
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		rm.Logger.Error("Failed to start ffmpeg for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		// Decrement input relay refcount on failure
		rm.StopInputRelay(inputName, inputURL)
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
	rm.Logger.Info("Started relay: %s [%s] -> %s [%s] (pid=%d)", inputName, inputURL, outputName, outputURL, cmd.Process.Pid)
	ep := &RelayEndpoint{
		OutputURL:      outputURL,
		OutputName:     outputName,
		Cmd:            cmd,
		Bitrate:        0.0,
		Status:         Running,
		PlatformPreset: preset,
		FFmpegOptions:  opts,
	}
	relay.Endpoints[outputURL] = ep
	relay.mu.Unlock()

	// Parse output in a goroutine (same as StartRelay)
	if stdout != nil {
		go func() {
			rm.Logger.Debug("Goroutine started: parsing ffmpeg output for %s [%s] -> %s [%s] (stdout)", inputName, inputURL, outputName, outputURL)
			reader := bufio.NewReader(stdout)
			bitrateRegex := regexp.MustCompile(`^bitrate=\s*([\d.]+)kbits/s`)
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					line = strings.TrimSpace(line)
					rm.Logger.Debug("ffmpeg STDOUT [%s [%s] -> %s [%s]]: %s", inputName, inputURL, outputName, outputURL, line)

					// Parse bitrate from progress output
					if matches := bitrateRegex.FindStringSubmatch(line); matches != nil {
						if bitrate, err := strconv.ParseFloat(matches[1], 64); err == nil {
							ep.mu.Lock()
							ep.Bitrate = bitrate
							ep.mu.Unlock()
							rm.Logger.Debug("Updated bitrate for %s [%s] -> %s [%s]: %f kbits/s", inputName, inputURL, outputName, outputURL, bitrate)
						}
					}
				}
				if err != nil {
					break
				}
			}
			rm.Logger.Debug("Goroutine exiting: ffmpeg output parsing for %s [%s] -> %s [%s] (stdout)", inputName, inputURL, outputName, outputURL)
		}()
	}
	if stderr != nil {
		go func() {
			rm.Logger.Debug("Goroutine started: parsing ffmpeg output for %s [%s] -> %s [%s] (stderr)", inputName, inputURL, outputName, outputURL)
			reader := bufio.NewReader(stderr)
			bitrateRegex := regexp.MustCompile(`bitrate=\s*([\d.]+)\s*kbits/s`)
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					rm.Logger.Info("ffmpeg STDERR [%s [%s] -> %s [%s]]: %s", inputName, inputURL, outputName, outputURL, strings.TrimSpace(line))
				}
				if err != nil {
					break
				}
				if matches := bitrateRegex.FindStringSubmatch(line); matches != nil {
					if bitrate, err := strconv.ParseFloat(matches[1], 64); err == nil {
						ep.mu.Lock()
						ep.Bitrate = bitrate
						ep.mu.Unlock()
						rm.Logger.Debug("Updated bitrate for %s [%s] -> %s [%s]: %f", inputName, inputURL, outputName, outputURL, bitrate)
						continue
					}
				}
			}
			rm.Logger.Debug("Goroutine exiting: ffmpeg output parsing for %s [%s] -> %s [%s] (stderr)", inputName, inputURL, outputName, outputURL)
		}()
	}

	// Monitor process (same as StartRelay)
	go func() {
		rm.Logger.Debug("Goroutine started: monitoring ffmpeg process for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
		err := cmd.Wait()
		ep.mu.Lock()
		prevStatus := ep.Status
		ep.Status = Stopped
		ep.Bitrate = 0.0
		ep.mu.Unlock()
		// Decrement input relay refcount when output relay stops
		rm.StopInputRelay(inputName, inputURL)
		if err != nil {
			if prevStatus == Running {
				rm.Logger.Error("ffmpeg exited with error for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
				ep.mu.Lock()
				ep.Status = Error
				ep.mu.Unlock()
			} else {
				rm.Logger.Info("ffmpeg process for %s [%s] -> %s [%s] was intentionally stopped", inputName, inputURL, outputName, outputURL)
			}
		} else {
			rm.Logger.Info("ffmpeg exited normally for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
		}
		rm.Logger.Debug("Goroutine exiting: monitoring ffmpeg process for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
	}()

	rm.Logger.Debug("StartRelayWithOptions finished: input=%s, output=%s", inputURL, outputURL)
	return nil
}

// StopRelay stops a relay endpoint for an input/output URL
func (rm *RelayManager) StopRelay(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("StopRelay called: input=%s, output=%s, input_name=%s, output_name=%s", inputURL, outputURL, inputName, outputName)
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	rm.mu.Unlock()
	if !exists {
		return fmt.Errorf("no relay for input %s", inputURL)
	}
	if relay.InputName != inputName {
		return fmt.Errorf("input name mismatch")
	}
	relay.mu.Lock()
	ep, exists := relay.Endpoints[outputURL]
	if !exists || ep.Status != Running {
		relay.mu.Unlock()
		return fmt.Errorf("relay not running for %s -> %s", inputURL, outputURL)
	}
	if ep.OutputName != outputName {
		relay.mu.Unlock()
		return fmt.Errorf("output name mismatch")
	}
	err := ep.Cmd.Process.Kill()
	if err != nil {
		relay.mu.Unlock()
		return fmt.Errorf("failed to stop relay for %s -> %s: %v", inputURL, outputURL, err)
	}
	ep.Status = Stopped
	relay.mu.Unlock()
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
	rm.mu.Lock()
	for _, relay := range rm.Relays {
		relay.mu.Lock()
		var outputs []struct {
			OutputURL      string            `json:"output_url"`
			OutputName     string            `json:"output_name"`
			PlatformPreset string            `json:"platform_preset,omitempty"`
			FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
		}
		for _, ep := range relay.Endpoints {
			output := struct {
				OutputURL      string            `json:"output_url"`
				OutputName     string            `json:"output_name"`
				PlatformPreset string            `json:"platform_preset,omitempty"`
				FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`
			}{
				OutputURL:      ep.OutputURL,
				OutputName:     ep.OutputName,
				PlatformPreset: ep.PlatformPreset,
			}

			// Convert FFmpegOptions to map for JSON serialization
			if ep.FFmpegOptions != nil {
				output.FFmpegOptions = make(map[string]string)
				if ep.FFmpegOptions.VideoCodec != "" {
					output.FFmpegOptions["video_codec"] = ep.FFmpegOptions.VideoCodec
				}
				if ep.FFmpegOptions.AudioCodec != "" {
					output.FFmpegOptions["audio_codec"] = ep.FFmpegOptions.AudioCodec
				}
				if ep.FFmpegOptions.Resolution != "" {
					output.FFmpegOptions["resolution"] = ep.FFmpegOptions.Resolution
				}
				if ep.FFmpegOptions.Framerate != "" {
					output.FFmpegOptions["framerate"] = ep.FFmpegOptions.Framerate
				}
				if ep.FFmpegOptions.Bitrate != "" {
					output.FFmpegOptions["bitrate"] = ep.FFmpegOptions.Bitrate
				}
				if ep.FFmpegOptions.Rotation != "" {
					output.FFmpegOptions["rotation"] = ep.FFmpegOptions.Rotation
				}
			}

			outputs = append(outputs, output)
		}
		configs = append(configs, exportConfig{
			InputURL:  relay.InputURL,
			InputName: relay.InputName,
			Outputs:   outputs,
		})
		relay.mu.Unlock()
	}
	rm.mu.Unlock()
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		rm.Logger.Error("Failed to marshal config: %v", err)
		return err
	}
	rm.Logger.Info("Exported relay config to %s", filename)
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

// StartInputRelay starts the input relay process if not running, increments refcount, returns local relay URL
func (rm *RelayManager) StartInputRelay(inputName, inputURL string) (string, error) {
	rm.Logger.Info("StartInputRelay: inputName=%s, inputURL=%s", inputName, inputURL)
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	if !exists {
		rm.Logger.Info("Creating new relay for input: %s", inputURL)
		relay = &Relay{
			InputURL:  inputURL,
			InputName: inputName,
			Endpoints: make(map[string]*RelayEndpoint),
			RefCount:  0,
			Status:    Stopped,
		}
		rm.Relays[inputURL] = relay
	}
	relay.mu.Lock()
	relay.RefCount++
	rm.Logger.Info("Relay refcount for %s is now %d", inputURL, relay.RefCount)

	// Check if input relay is already starting or running
	if relay.Status == Starting {
		// Another goroutine is already starting this relay, just wait
		rm.Logger.Debug("Input relay for %s is already starting, waiting...", inputURL)
		localURL := relay.LocalRelayURL
		relay.mu.Unlock()
		rm.mu.Unlock()
		return localURL, nil
	}

	if relay.LocalRelayCmd == nil || relay.Status == Stopped {
		relay.Status = Starting // Mark as starting to prevent race conditions
		relayPath := fmt.Sprintf("relay/%s", inputName)
		relay.LocalRelayURL = fmt.Sprintf("%s/%s", GetRTSPServerURL(), relayPath)

		// Create empty stream in RTSP server if available
		if rm.rtspServer != nil {
			streamURL, err := rm.rtspServer.CreateEmptyStream(relayPath)
			if err != nil {
				rm.Logger.Error("Failed to create RTSP stream: %v", err)
				relay.RefCount--
				relay.mu.Unlock()
				rm.mu.Unlock()
				return "", err
			}
			rm.Logger.Debug("Created RTSP stream: %s", streamURL)
		}

		rm.Logger.Info("Starting input relay ffmpeg: %s -> %s", inputURL, relay.LocalRelayURL)
		// Use RTSP publishing format for ffmpeg to push to RTSP server
		cmd := exec.Command("ffmpeg",
			"-i", inputURL,
			"-c", "copy",
			"-f", "rtsp",
			"-rtsp_transport", "tcp",
			relay.LocalRelayURL)
		relay.LocalRelayCmd = cmd
		relay.Status = Running
		go rm.RunInputRelay(relay)
	}
	localURL := relay.LocalRelayURL
	relay.mu.Unlock()
	rm.mu.Unlock()
	return localURL, nil
}

// StopInputRelay decrements refcount and stops input relay if no outputs/recordings remain
func (rm *RelayManager) StopInputRelay(inputName, inputURL string) {
	rm.Logger.Info("StopInputRelay: inputName=%s, inputURL=%s", inputName, inputURL)
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	if !exists {
		rm.Logger.Warn("StopInputRelay: relay for %s not found", inputURL)
		rm.mu.Unlock()
		return
	}
	relay.mu.Lock()
	relay.RefCount--
	rm.Logger.Info("Relay refcount for %s is now %d", inputURL, relay.RefCount)
	if relay.RefCount <= 0 {
		rm.Logger.Info("No more consumers, stopping input relay for %s", inputURL)
		if relay.LocalRelayCmd != nil && relay.LocalRelayCmd.Process != nil {
			err := relay.LocalRelayCmd.Process.Kill()
			if err != nil {
				rm.Logger.Error("Failed to kill input relay process for %s: %v", inputURL, err)
			}
		}

		// Remove stream from RTSP server if available
		if rm.rtspServer != nil {
			relayPath := fmt.Sprintf("relay/%s", inputName)
			rm.rtspServer.RemoveStream(relayPath)
		}

		relay.Status = Stopped
		relay.LocalRelayCmd = nil
	}
	relay.mu.Unlock()
	rm.mu.Unlock()
}

// RunInputRelay runs and monitors the input relay process, restarts on failure if still needed
func (rm *RelayManager) RunInputRelay(relay *Relay) {
	rm.Logger.Info("RunInputRelay: running ffmpeg for %s -> %s", relay.InputURL, relay.LocalRelayURL)
	retryDelay := 5     // seconds
	maxRetryDelay := 60 // seconds
	for {
		err := relay.LocalRelayCmd.Run()
		relay.mu.Lock()
		if relay.Status == Stopped || relay.RefCount <= 0 {
			if err != nil {
				// Process was intentionally killed, this is expected
				rm.Logger.Info("Input relay for %s stopped (signal: %v)", relay.InputURL, err)
			} else {
				rm.Logger.Info("Input relay for %s stopped cleanly", relay.InputURL)
			}
			rm.Logger.Info("Input relay for %s stopped or no consumers, exiting RunInputRelay", relay.InputURL)
			relay.mu.Unlock()
			return
		}
		if err != nil {
			rm.Logger.Error("Input relay process exited with error for %s: %v", relay.InputURL, err)
		}
		rm.Logger.Warn("Input relay for %s exited unexpectedly, restarting in %ds", relay.InputURL, retryDelay)
		relay.Status = Error
		relay.LocalRelayCmd = exec.Command("ffmpeg",
			"-hide_banner", "-loglevel", "error", // Only show actual errors
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "10",
			"-i", relay.InputURL, "-c", "copy", "-f", "rtsp", relay.LocalRelayURL)
		relay.mu.Unlock()

		// Exponential backoff with jitter for retry delay
		time.Sleep(time.Duration(retryDelay) * time.Second)
		retryDelay = min(retryDelay*2, maxRetryDelay)
	}
}

func (rm *RelayManager) Status() status.FullStatus {
	srv, _ := process.GetSelfUsage()
	serverStatus := status.ServerStatus{}
	if srv != nil {
		serverStatus = status.ServerStatus{CPU: srv.CPU, Mem: srv.Mem}
	}

	relays := []status.RelayStatus{}
	rm.mu.Lock()
	for _, relay := range rm.Relays {
		relay.mu.Lock()
		endpoints := []status.EndpointStatus{}
		for _, ep := range relay.Endpoints {
			ep.mu.Lock()
			cpuVal := 0.0
			memVal := uint64(0)
			if ep.Cmd != nil && ep.Cmd.Process != nil {
				if u, err := process.GetProcUsage(ep.Cmd.Process.Pid); err == nil {
					cpuVal = u.CPU
					memVal = u.Mem
				}
			}
			var statusStr string
			switch ep.Status {
			case Starting:
				statusStr = "Starting"
			case Running:
				statusStr = "Running"
			case Error:
				statusStr = "Error"
			default:
				statusStr = "Stopped"
			}
			endpoints = append(endpoints, status.EndpointStatus{
				OutputURL:  ep.OutputURL,
				OutputName: ep.OutputName,
				Status:     statusStr,
				Bitrate:    ep.Bitrate,
				CPU:        cpuVal,
				Mem:        memVal,
			})
			ep.mu.Unlock()
		}
		relays = append(relays, status.RelayStatus{
			InputURL:  relay.InputURL,
			InputName: relay.InputName,
			Endpoints: endpoints,
		})
		relay.mu.Unlock()
	}
	rm.mu.Unlock()
	return status.FullStatus{
		Server: serverStatus,
		Relays: relays,
	}
}

// GetEndpointConfig retrieves the stored platform preset and ffmpeg options for an existing endpoint
func (rm *RelayManager) GetEndpointConfig(inputURL, outputURL string) (string, *FFmpegOptions, error) {
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	rm.mu.Unlock()
	if !exists {
		return "", nil, fmt.Errorf("no relay for input %s", inputURL)
	}

	relay.mu.Lock()
	defer relay.mu.Unlock()

	ep, exists := relay.Endpoints[outputURL]
	if !exists {
		return "", nil, fmt.Errorf("no endpoint for output %s", outputURL)
	}

	return ep.PlatformPreset, ep.FFmpegOptions, nil
}
