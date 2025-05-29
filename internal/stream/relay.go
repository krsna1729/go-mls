package stream

import (
	"fmt"
	"os/exec"
	"sync"
)

type RTMPRelayConfig struct {
	InputURL  string `json:"input_url"`
	OutputURL string `json:"output_url"`
}

type StreamManager struct {
	cmd         *exec.Cmd
	mu          sync.Mutex
	Active      bool
	LastCmd     string
	RelayConfig RTMPRelayConfig
}

func NewStreamManager() *StreamManager {
	return &StreamManager{}
}

func (s *StreamManager) StartRelay(cfg RTMPRelayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Active {
		return nil // Already running
	}
	// Example: Use ffmpeg to relay RTMP (replace with Go-native RTMP if desired)
	cmd := exec.Command("ffmpeg", "-re", "-i", cfg.InputURL, "-c", "copy", "-f", "flv", cfg.OutputURL)
	s.LastCmd = fmt.Sprintf("ffmpeg -re -i %s -c copy -f flv %s", cfg.InputURL, cfg.OutputURL)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	s.Active = true
	s.RelayConfig = cfg
	return nil
}

func (s *StreamManager) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Active || s.cmd == nil {
		return nil // Not running
	}
	if err := s.cmd.Process.Kill(); err != nil {
		return err
	}
	s.Active = false
	s.cmd = nil
	return nil
}

func (s *StreamManager) Status() (bool, RTMPRelayConfig, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Active, s.RelayConfig, s.LastCmd
}
