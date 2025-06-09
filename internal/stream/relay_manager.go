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

	"go-mls/internal/logger"
	"go-mls/internal/process"
	"go-mls/internal/status"
)

// RelayEndpoint manages a single output URL and its ffmpeg process
type Status int

const (
	Running Status = iota
	Stopped
	Error
)

type RelayEndpoint struct {
	OutputURL  string
	OutputName string
	Cmd        *exec.Cmd
	Status     Status
	Bitrate    float64 // in kbits/s
	mu         sync.Mutex
}

// Relay manages all endpoints for a single input URL
type Relay struct {
	InputURL  string
	InputName string
	Endpoints map[string]*RelayEndpoint // key: output URL
	mu        sync.Mutex
}

// RelayManager manages all relays (per input URL)
type RelayManager struct {
	Relays map[string]*Relay // key: input URL
	mu     sync.Mutex
	Logger *logger.Logger
}

func NewRelayManager(l *logger.Logger) *RelayManager {
	return &RelayManager{
		Relays: make(map[string]*Relay),
		Logger: l,
	}
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
	args := []string{"-re", "-i", inputURL}
	var o FFmpegOptions
	if preset, ok := PlatformPresets[presetName]; ok {
		o = preset.Options
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
	if o.Framerate != "" {
		args = append(args, "-r", o.Framerate)
	}
	if o.Bitrate != "" {
		args = append(args, "-b:v", o.Bitrate)
	}
	if o.Rotation != "" {
		args = append(args, "-vf", o.Rotation)
	}
	if len(o.ExtraArgs) > 0 {
		args = append(args, o.ExtraArgs...)
	}
	args = append(args, "-f", "flv", outputURL)
	return exec.Command("ffmpeg", args...)
}

// buildFFmpegCmd is now a wrapper for backward compatibility
func buildFFmpegCmd(inputURL, outputURL string) *exec.Cmd {
	return buildFFmpegCmdWithOptions(inputURL, outputURL, nil, "")
}

// Helper to get a platform preset by name
func GetPlatformPreset(name string) (PlatformPreset, bool) {
	preset, ok := PlatformPresets[name]
	return preset, ok
}

// StartRelay starts a relay for an input/output URL and stores names
func (rm *RelayManager) StartRelay(inputURL, outputURL, inputName, outputName string) error {
	rm.Logger.Debug("StartRelay called: input=%s, output=%s, input_name=%s, output_name=%s", inputURL, outputURL, inputName, outputName)
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

	// Remove Running from RelayEndpoint, use Status field
	relay.mu.Lock()
	if ep, exists := relay.Endpoints[outputURL]; exists && ep.Status == Running {
		rm.Logger.Warn("Relay already running for %s [%s] -> %s [%s]", relay.InputName, inputURL, ep.OutputName, outputURL)
		relay.mu.Unlock()
		return fmt.Errorf("relay already running for %s -> %s", inputURL, outputURL)
	}
	cmd := buildFFmpegCmd(inputURL, outputURL)
	rm.Logger.Debug("Starting ffmpeg process: %v", cmd.Args)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rm.Logger.Error("Failed to create stderr pipe for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		rm.Logger.Error("Failed to start ffmpeg for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
	rm.Logger.Info("Started relay: %s [%s] -> %s [%s] (pid=%d)", inputName, inputURL, outputName, outputURL, cmd.Process.Pid)
	ep := &RelayEndpoint{
		OutputURL:  outputURL,
		OutputName: outputName,
		Cmd:        cmd,
		Bitrate:    0.0,
		Status:     Running,
	}
	relay.Endpoints[outputURL] = ep
	relay.mu.Unlock()

	// Parse output in a goroutine
	go func() {
		rm.Logger.Debug("Goroutine started: parsing ffmpeg output for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
		reader := bufio.NewReader(stderr)
		bitrateRegex := regexp.MustCompile(`bitrate=\s*([\d.]+)\s*kbits/s`)
		for {
			line, err := reader.ReadString('\r')
			if err != nil {
				break
			}
			line = strings.Trim(line, "\r\n")
			rm.Logger.Debug("ffmpeg [%s [%s] -> %s [%s]]: %s", inputName, inputURL, outputName, outputURL, line)
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
		rm.Logger.Debug("Goroutine exiting: ffmpeg output parsing for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
	}()

	// Monitor process
	go func() {
		rm.Logger.Debug("Goroutine started: monitoring ffmpeg process for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
		err := cmd.Wait()
		ep.mu.Lock()
		// Reset state on exit
		prevStatus := ep.Status
		ep.Status = Stopped
		ep.Bitrate = 0.0
		ep.mu.Unlock()
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

	rm.Logger.Debug("StartRelay finished: input=%s, output=%s", inputURL, outputURL)
	return nil
}

// StartRelayWithOptions starts a relay with advanced ffmpeg options and/or platform preset
func (rm *RelayManager) StartRelayWithOptions(inputURL, outputURL, inputName, outputName string, opts *FFmpegOptions, preset string) error {
	rm.Logger.Debug("StartRelayWithOptions called: input=%s, output=%s, input_name=%s, output_name=%s, preset=%s", inputURL, outputURL, inputName, outputName, preset)
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
	cmd := buildFFmpegCmdWithOptions(inputURL, outputURL, opts, preset)
	rm.Logger.Debug("Starting ffmpeg process: %v", cmd.Args)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rm.Logger.Error("Failed to create stderr pipe for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		rm.Logger.Error("Failed to start ffmpeg for %s [%s] -> %s [%s]: %v", inputName, inputURL, outputName, outputURL, err)
		relay.mu.Unlock()
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
	rm.Logger.Info("Started relay: %s [%s] -> %s [%s] (pid=%d)", inputName, inputURL, outputName, outputURL, cmd.Process.Pid)
	ep := &RelayEndpoint{
		OutputURL:  outputURL,
		OutputName: outputName,
		Cmd:        cmd,
		Bitrate:    0.0,
		Status:     Running,
	}
	relay.Endpoints[outputURL] = ep
	relay.mu.Unlock()

	// Parse output in a goroutine (same as StartRelay)
	go func() {
		rm.Logger.Debug("Goroutine started: parsing ffmpeg output for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
		reader := bufio.NewReader(stderr)
		bitrateRegex := regexp.MustCompile(`bitrate=\s*([\d.]+)\s*kbits/s`)
		for {
			line, err := reader.ReadString('\r')
			if err != nil {
				break
			}
			line = strings.Trim(line, "\r\n")
			rm.Logger.Debug("ffmpeg [%s [%s] -> %s [%s]]: %s", inputName, inputURL, outputName, outputURL, line)
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
		rm.Logger.Debug("Goroutine exiting: ffmpeg output parsing for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
	}()

	// Monitor process (same as StartRelay)
	go func() {
		rm.Logger.Debug("Goroutine started: monitoring ffmpeg process for %s [%s] -> %s [%s]", inputName, inputURL, outputName, outputURL)
		err := cmd.Wait()
		ep.mu.Lock()
		prevStatus := ep.Status
		ep.Status = Stopped
		ep.Bitrate = 0.0
		ep.mu.Unlock()
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

// ExportConfig saves the current relay configurations to a file (now includes names)
func (rm *RelayManager) ExportConfig(filename string) error {
	rm.Logger.Debug("ExportConfig called: filename=%s", filename)
	type exportConfig struct {
		InputURL  string `json:"input_url"`
		InputName string `json:"input_name"`
		Outputs   []struct {
			OutputURL  string `json:"output_url"`
			OutputName string `json:"output_name"`
		} `json:"outputs"`
	}
	var configs []exportConfig
	rm.mu.Lock()
	for _, relay := range rm.Relays {
		relay.mu.Lock()
		var outputs []struct {
			OutputURL  string `json:"output_url"`
			OutputName string `json:"output_name"`
		}
		for _, ep := range relay.Endpoints {
			outputs = append(outputs, struct {
				OutputURL  string `json:"output_url"`
				OutputName string `json:"output_name"`
			}{ep.OutputURL, ep.OutputName})
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
	for _, relayCfg := range configs {
		for _, out := range relayCfg.Outputs {
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
			err := rm.StartRelayWithOptions(relayCfg.InputURL, out.OutputURL, relayCfg.InputName, out.OutputName, opts, out.PlatformPreset)
			if err != nil {
				rm.Logger.Error("Failed to start relay: %v", err)
			}
		}
	}
	rm.Logger.Info("Imported relay config from %s", filename)
	return nil
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
