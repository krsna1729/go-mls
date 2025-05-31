package stream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
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
}

func NewRelayManager() *RelayManager {
	return &RelayManager{
		Relays: make(map[string]*Relay),
	}
}

// StartRelay starts a relay for an input URL and output URL
func (rm *RelayManager) StartRelay(inputURL, outputURL string) error {
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	if !exists {
		relay = &Relay{
			InputURL:  inputURL,
			Endpoints: make(map[string]*RelayEndpoint),
		}
		rm.Relays[inputURL] = relay
	}
	rm.mu.Unlock()

	relay.mu.Lock()
	if ep, exists := relay.Endpoints[outputURL]; exists && ep.Running {
		relay.mu.Unlock()
		return fmt.Errorf("relay already running for %s -> %s", inputURL, outputURL)
	}
	cmd := exec.Command("ffmpeg", "-re", "-i", inputURL, "-c", "copy", "-f", "flv", outputURL)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		relay.mu.Unlock()
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		relay.mu.Unlock()
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
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
		scanner := bufio.NewScanner(stderr)
		bitrateRegex := regexp.MustCompile(`bitrate=([\d.]+)kbits/s`)
		for scanner.Scan() {
			line := scanner.Text()
			if matches := bitrateRegex.FindStringSubmatch(line); matches != nil {
				if bitrate, err := strconv.ParseFloat(matches[1], 64); err == nil {
					ep.mu.Lock()
					ep.Bitrate = bitrate
					ep.mu.Unlock()
				}
			}
		}
	}()

	// Monitor process
	go func() {
		err := cmd.Wait()
		ep.mu.Lock()
		ep.Running = false
		ep.mu.Unlock()
		if err != nil {
			// Optionally: restart or log error
		}
	}()

	return nil
}

// StopRelay stops a relay endpoint for an input/output URL
func (rm *RelayManager) StopRelay(inputURL, outputURL string) error {
	rm.mu.Lock()
	relay, exists := rm.Relays[inputURL]
	rm.mu.Unlock()
	if !exists {
		return fmt.Errorf("no relay for input %s", inputURL)
	}
	relay.mu.Lock()
	ep, exists := relay.Endpoints[outputURL]
	relay.mu.Unlock()
	if !exists || !ep.Running {
		return fmt.Errorf("relay not running for %s -> %s", inputURL, outputURL)
	}
	return ep.Cmd.Process.Kill()
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
	return statuses
}

// ExportConfig saves the current relay configurations to a file (grouped by input URL)
func (rm *RelayManager) ExportConfig(filename string) error {
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
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// ImportConfig loads relay configurations from a file (grouped by input URL)
func (rm *RelayManager) ImportConfig(filename string) error {
	type importConfig map[string][]string
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var cfg importConfig
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return err
	}
	for input, outputs := range cfg {
		for _, output := range outputs {
			rm.StartRelay(input, output)
		}
	}
	return nil
}
