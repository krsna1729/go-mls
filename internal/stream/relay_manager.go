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
type RelayEndpoint struct {
	OutputURL string
	Cmd       *exec.Cmd
	Running   bool
	Bitrate   float64 // in kbits/s
	mu        sync.Mutex
}

// Relay manages all endpoints for a single input URL
type Relay struct {
	InputURL  string
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

func buildFFmpegCmd(inputURL, outputURL string) *exec.Cmd {
	return exec.Command("ffmpeg", "-re", "-i", inputURL, "-c", "copy", "-f", "flv", outputURL)
}

// StartRelay starts a relay for an input URL and output URL
func (rm *RelayManager) StartRelay(inputURL, outputURL string) error {
	rm.Logger.Debug("StartRelay called: input=%s, output=%s", inputURL, outputURL)
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	if !exists {
		rm.Logger.Debug("No existing relay for input %s, creating new Relay struct", inputURL)
		relay = &Relay{
			InputURL:  inputURL,
			Endpoints: make(map[string]*RelayEndpoint),
		}
		rm.Relays[inputURL] = relay
		rm.Logger.Info("Created new relay for input: %s", inputURL)
	}
	rm.mu.Unlock()

	relay.mu.Lock()
	if ep, exists := relay.Endpoints[outputURL]; exists && ep.Running {
		rm.Logger.Warn("Relay already running for %s -> %s", inputURL, outputURL)
		relay.mu.Unlock()
		return fmt.Errorf("relay already running for %s -> %s", inputURL, outputURL)
	}
	cmd := buildFFmpegCmd(inputURL, outputURL)
	rm.Logger.Debug("Starting ffmpeg process: %v", cmd.Args)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rm.Logger.Error("Failed to create stderr pipe for %s -> %s: %v", inputURL, outputURL, err)
		relay.mu.Unlock()
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		rm.Logger.Error("Failed to start ffmpeg for %s -> %s: %v", inputURL, outputURL, err)
		relay.mu.Unlock()
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
	rm.Logger.Info("Started relay: %s -> %s (pid=%d)", inputURL, outputURL, cmd.Process.Pid)
	ep := &RelayEndpoint{
		OutputURL: outputURL,
		Cmd:       cmd,
		Running:   true,
		Bitrate:   0.0,
	}
	relay.Endpoints[outputURL] = ep
	relay.mu.Unlock()

	// Parse output in a goroutine
	go func() {
		rm.Logger.Debug("Goroutine started: parsing ffmpeg output for %s -> %s", inputURL, outputURL)
		reader := bufio.NewReader(stderr)
		bitrateRegex := regexp.MustCompile(`bitrate=\s*([\d.]+)\s*kbits/s`)
		for {
			line, err := reader.ReadString('\r')
			if err != nil {
				break
			}
			line = strings.Trim(line, "\r\n") // Ensure line endings are trimmed
			rm.Logger.Debug("ffmpeg [%s -> %s]: %s", inputURL, outputURL, line)
			if matches := bitrateRegex.FindStringSubmatch(line); matches != nil {
				if bitrate, err := strconv.ParseFloat(matches[1], 64); err == nil {
					ep.mu.Lock()
					ep.Bitrate = bitrate
					ep.mu.Unlock()
					rm.Logger.Debug("Updated bitrate for %s -> %s: %f", inputURL, outputURL, bitrate)
					continue
				}
			}
		}
		rm.Logger.Debug("Goroutine exiting: ffmpeg output parsing for %s -> %s", inputURL, outputURL)
	}()

	// Monitor process
	go func() {
		rm.Logger.Debug("Goroutine started: monitoring ffmpeg process for %s -> %s", inputURL, outputURL)
		err := cmd.Wait()
		ep.mu.Lock()
		// Reset state on exit
		ep.Running = false
		ep.Bitrate = 0.0
		ep.mu.Unlock()
		if err != nil {
			rm.Logger.Error("ffmpeg exited with error for %s -> %s: %v", inputURL, outputURL, err)
		} else {
			rm.Logger.Info("ffmpeg exited normally for %s -> %s", inputURL, outputURL)
		}
		rm.Logger.Debug("Goroutine exiting: monitoring ffmpeg process for %s -> %s", inputURL, outputURL)
	}()

	rm.Logger.Debug("StartRelay finished: input=%s, output=%s", inputURL, outputURL)
	return nil
}

// StopRelay stops a relay endpoint for an input/output URL
func (rm *RelayManager) StopRelay(inputURL, outputURL string) error {
	rm.Logger.Debug("StopRelay called: input=%s, output=%s", inputURL, outputURL)
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	rm.mu.Unlock()
	if !exists {
		rm.Logger.Warn("No relay for input %s", inputURL)
		return fmt.Errorf("no relay for input %s", inputURL)
	}
	relay.mu.Lock()
	ep, exists := relay.Endpoints[outputURL]
	relay.mu.Unlock()
	if !exists || !ep.Running {
		rm.Logger.Warn("Relay not running for %s -> %s", inputURL, outputURL)
		return fmt.Errorf("relay not running for %s -> %s", inputURL, outputURL)
	}
	err := ep.Cmd.Process.Kill()
	if err != nil {
		rm.Logger.Error("Failed to stop relay for %s -> %s: %v", inputURL, outputURL, err)
		return err
	}
	rm.Logger.Info("Stopped relay: %s -> %s", inputURL, outputURL)
	return nil
}

// Status returns the status and bitrate of all relays
type RelayStatus struct {
	InputURL  string `json:"input_url"`
	Endpoints []struct {
		OutputURL string  `json:"output_url"`
		Running   bool    `json:"running"`
		Bitrate   float64 `json:"bitrate"`
	} `json:"endpoints"`
}

func (rm *RelayManager) Status() []RelayStatus {
	rm.Logger.Debug("Status called")
	statuses := []RelayStatus{}
	rm.mu.Lock()
	for _, relay := range rm.Relays {
		relay.mu.Lock()
		endpoints := []struct {
			OutputURL string  `json:"output_url"`
			Running   bool    `json:"running"`
			Bitrate   float64 `json:"bitrate"`
		}{}
		for _, ep := range relay.Endpoints {
			ep.mu.Lock()
			endpoints = append(endpoints, struct {
				OutputURL string  `json:"output_url"`
				Running   bool    `json:"running"`
				Bitrate   float64 `json:"bitrate"`
			}{OutputURL: ep.OutputURL, Running: ep.Running, Bitrate: ep.Bitrate})
			ep.mu.Unlock()
		}
		statuses = append(statuses, RelayStatus{
			InputURL:  relay.InputURL,
			Endpoints: endpoints,
		})
		relay.mu.Unlock()
	}
	rm.mu.Unlock()
	rm.Logger.Debug("Status returning %d relays", len(statuses))
	return statuses
}

// ExportConfig saves the current relay configurations to a file (grouped by input URL)
func (rm *RelayManager) ExportConfig(filename string) error {
	rm.Logger.Debug("ExportConfig called: filename=%s", filename)
	type exportConfig map[string][]string // input_url -> []output_url
	rm.mu.Lock()
	cfg := make(exportConfig)
	for _, relay := range rm.Relays {
		relay.mu.Lock()
		var outputs []string
		for _, ep := range relay.Endpoints {
			outputs = append(outputs, ep.OutputURL)
		}
		cfg[relay.InputURL] = outputs
		relay.mu.Unlock()
	}
	rm.mu.Unlock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		rm.Logger.Error("Failed to marshal config: %v", err)
		return err
	}
	rm.Logger.Info("Exported relay config to %s", filename)
	return os.WriteFile(filename, data, 0644)
}

// ImportConfig loads relay configurations from a file (grouped by input URL)
func (rm *RelayManager) ImportConfig(filename string) error {
	rm.Logger.Debug("ImportConfig called: filename=%s", filename)
	type importConfig map[string][]string
	data, err := os.ReadFile(filename)
	if err != nil {
		rm.Logger.Error("Failed to read file %s: %v", filename, err)
		return err
	}
	var cfg importConfig
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		rm.Logger.Error("Failed to unmarshal config: %v", err)
		return err
	}
	for input, outputs := range cfg {
		for _, output := range outputs {
			rm.Logger.Debug("ImportConfig: starting relay for input=%s, output=%s", input, output)
			rm.StartRelay(input, output)
		}
	}
	rm.Logger.Info("Imported relay config from %s", filename)
	return nil
}

func (rm *RelayManager) StatusFull() status.FullStatus {
	srv, _ := process.GetSelfUsage()
	serverStatus := status.ServerStatus{}
	if srv != nil {
		serverStatus = status.ServerStatus{CPU: srv.CPU, Mem: srv.Mem, PID: srv.PID}
	}

	relays := []status.RelayStatusFull{}
	rm.mu.Lock()
	for _, relay := range rm.Relays {
		relay.mu.Lock()
		endpoints := []status.EndpointStatus{}
		for _, ep := range relay.Endpoints {
			ep.mu.Lock()
			pid := 0
			cpu := 0.0
			mem := uint64(0)
			if ep.Cmd != nil && ep.Cmd.Process != nil {
				pid = ep.Cmd.Process.Pid
				if u, err := process.GetProcUsage(pid); err == nil {
					cpu = u.CPU
					mem = u.Mem
				}
			}
			endpoints = append(endpoints, status.EndpointStatus{
				OutputURL: ep.OutputURL,
				Running:   ep.Running,
				Bitrate:   ep.Bitrate,
				PID:       pid,
				CPU:       cpu,
				Mem:       mem,
			})
			ep.mu.Unlock()
		}
		relays = append(relays, status.RelayStatusFull{
			InputURL:  relay.InputURL,
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
