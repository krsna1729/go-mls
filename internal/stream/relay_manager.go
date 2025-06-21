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

// RelayManager manages all relays (per input URL)
type RelayManager struct {
	InputRelays  *InputRelayManager
	OutputRelays *OutputRelayManager
	Logger       *logger.Logger
	rtspServer   *RTSPServerManager // RTSP server for local relays
	recDir       string             // Directory for playing recordings from
}

func NewRelayManager(l *logger.Logger, recDir string) *RelayManager {
	irm := NewInputRelayManager(l, recDir)
	orm := NewOutputRelayManager(l)
	rm := &RelayManager{
		InputRelays:  irm,
		OutputRelays: orm,
		Logger:       l,
		recDir:       recDir,
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

	// Compose local RTSP relay path and URL
	relayPath := fmt.Sprintf("relay/%s", inputName)
	localRelayURL := fmt.Sprintf("%s/%s", GetRTSPServerURL(), relayPath)

	// Start or get the input relay
	inputTimeout := 30 * time.Second // TODO: make configurable
	_, err := rm.InputRelays.StartInputRelay(inputName, inputURL, localRelayURL, inputTimeout)
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

	outputTimeout := 60 * time.Second // TODO: make configurable and > inputTimeout
	config := OutputRelayConfig{
		OutputURL:      outputURL,
		OutputName:     outputName,
		InputURL:       inputURL,
		LocalURL:       localRelayURL,
		Timeout:        outputTimeout,
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
		if in.Cmd != nil && in.Cmd.Process != nil {
			if usage, err := process.GetProcUsage(in.Cmd.Process.Pid); err == nil {
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
				if out.Cmd != nil && out.Cmd.Process != nil {
					if usage, err := process.GetProcUsage(out.Cmd.Process.Pid); err == nil {
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
