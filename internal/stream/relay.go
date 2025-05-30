package stream

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

const logBufferSize = 100

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
	logLines    []string
}

func NewStreamManager() *StreamManager {
	return &StreamManager{}
}

func (s *StreamManager) appendLog(line string) {
	if len(s.logLines) >= logBufferSize {
		s.logLines = s.logLines[1:]
	}
	s.logLines = append(s.logLines, line)
}

func (s *StreamManager) StartRelay(cfg RTMPRelayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Active {
		return nil // Already running
	}
	cmd := exec.Command("ffmpeg", "-re", "-i", cfg.InputURL, "-c", "copy", "-f", "flv", cfg.OutputURL)
	s.LastCmd = fmt.Sprintf("ffmpeg -re -i %s -c copy -f flv %s", cfg.InputURL, cfg.OutputURL)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}
	s.cmd = cmd
	s.Active = true
	s.RelayConfig = cfg
	s.logLines = nil // reset logs

	// Stream logs in real time
	go func() {
		stdoutReader := bufio.NewReader(stdoutPipe)
		for {
			line, err := stdoutReader.ReadString('\r')
			line = strings.Trim(line, "\r\n")
			if len(line) > 0 {
				s.mu.Lock()
				s.appendLog(line)
				s.mu.Unlock()
			}
			if err != nil {
				if err == io.EOF {
					break
				}
			}
		}
	}()
	go func() {
		for {
			line, err := bufio.NewReader(stderrPipe).ReadString('\r')
			line = strings.Trim(line, "\r\n")
			if len(line) > 0 {
				s.mu.Lock()
				s.appendLog("[ERR] " + line)
				s.mu.Unlock()
			}
			if err != nil {
				if err == io.EOF {
					break
				}
			}
		}
	}()
	go func() {
		cmd.Wait()
		s.mu.Lock()
		s.Active = false
		s.mu.Unlock()
	}()
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

func (s *StreamManager) GetLogs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logLines...)
}
