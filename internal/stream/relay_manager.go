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

type RelayPair struct {
	InputURL  string
	OutputURL string
	Cmd       *exec.Cmd
	Running   bool
	Bitrate   float64 // in kbits/s
	mu        sync.Mutex
}

type RelayManager struct {
	Pairs map[string]*RelayPair // Key: "inputURL|outputURL"
	mu    sync.Mutex
}

func NewRelayManager() *RelayManager {
	return &RelayManager{
		Pairs: make(map[string]*RelayPair),
	}
}

func relayKey(input, output string) string {
	return input + "|" + output
}

// StartRelay starts an FFmpeg process for a given input-output pair and monitors it
func (rm *RelayManager) StartRelay(inputURL, outputURL string) error {
	rm.mu.Lock()
	key := relayKey(inputURL, outputURL)
	if pair, exists := rm.Pairs[key]; exists && pair.Running {
		rm.mu.Unlock()
		return fmt.Errorf("relay %s already running", key)
	}
	cmd := exec.Command("ffmpeg", "-re", "-i", inputURL, "-c", "copy", "-f", "flv", outputURL)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rm.mu.Unlock()
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		rm.mu.Unlock()
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}
	pair := &RelayPair{
		InputURL:  inputURL,
		OutputURL: outputURL,
		Cmd:       cmd,
		Running:   true,
		Bitrate:   0.0,
	}
	rm.Pairs[key] = pair
	rm.mu.Unlock()

	// Parse output in a goroutine
	go func() {
		scanner := bufio.NewScanner(stderr)
		bitrateRegex := regexp.MustCompile(`bitrate=([\d.]+)kbits/s`)
		for scanner.Scan() {
			line := scanner.Text()
			if matches := bitrateRegex.FindStringSubmatch(line); matches != nil {
				if bitrate, err := strconv.ParseFloat(matches[1], 64); err == nil {
					pair.mu.Lock()
					pair.Bitrate = bitrate
					pair.mu.Unlock()
				}
			}
		}
	}()

	// Monitor process
	go func() {
		err := cmd.Wait()
		pair.mu.Lock()
		pair.Running = false
		pair.mu.Unlock()
		if err != nil {
			// Optionally: restart or log error
		}
	}()

	return nil
}

// StopRelay stops an FFmpeg process for a given input-output pair
func (rm *RelayManager) StopRelay(inputURL, outputURL string) error {
	key := relayKey(inputURL, outputURL)
	rm.mu.Lock()
	pair, exists := rm.Pairs[key]
	rm.mu.Unlock()
	if !exists || !pair.Running {
		return fmt.Errorf("relay %s not running", key)
	}
	return pair.Cmd.Process.Kill()
}

// Status returns the status and bitrate of all relays
type RelayStatus struct {
	Running bool    `json:"running"`
	Bitrate float64 `json:"bitrate"`
}

func (rm *RelayManager) Status() map[string]RelayStatus {
	status := make(map[string]RelayStatus)
	rm.mu.Lock()
	for key, pair := range rm.Pairs {
		pair.mu.Lock()
		status[key] = RelayStatus{Running: pair.Running, Bitrate: pair.Bitrate}
		pair.mu.Unlock()
	}
	rm.mu.Unlock()
	return status
}

// RelayConfigExport is a serializable struct for exporting relay configs
type RelayConfigExport struct {
	InputURL  string `json:"input_url"`
	OutputURL string `json:"output_url"`
}

// ExportConfig saves the current relay configurations to a file (only serializable fields)
func (rm *RelayManager) ExportConfig(filename string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	exportMap := make(map[string]RelayConfigExport)
	for key, pair := range rm.Pairs {
		exportMap[key] = RelayConfigExport{
			InputURL:  pair.InputURL,
			OutputURL: pair.OutputURL,
		}
	}
	data, err := json.MarshalIndent(exportMap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// ImportConfig loads relay configurations from a file and starts them
func (rm *RelayManager) ImportConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var pairs map[string]*RelayPair
	err = json.Unmarshal(data, &pairs)
	if err != nil {
		return err
	}
	rm.mu.Lock()
	for key, pair := range pairs {
		rm.Pairs[key] = pair
		rm.mu.Unlock()
		rm.StartRelay(pair.InputURL, pair.OutputURL)
		rm.mu.Lock()
	}
	rm.mu.Unlock()
	return nil
}
