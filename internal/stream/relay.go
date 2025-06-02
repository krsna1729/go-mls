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
	InputURL   string   `json:"input_url"`
	OutputURLs []string `json:"output_urls"`
}

// StreamManager now manages multiple ffmpeg processes (one per output)
type StreamManager struct {
	cmds        []*exec.Cmd
	mu          sync.Mutex
	Active      bool
	LastCmds    []string
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
	if len(cfg.OutputURLs) == 0 {
		return fmt.Errorf("no output URLs specified")
	}
	s.cmds = nil
	s.LastCmds = nil
	s.logLines = nil // reset logs

	for _, outURL := range cfg.OutputURLs {
		cmd := buildFFmpegCmd(cfg.InputURL, outURL)
		lastCmd := fmt.Sprintf("ffmpeg -re -i %s -c copy -f flv %s", cfg.InputURL, outURL)
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
		s.cmds = append(s.cmds, cmd)
		s.LastCmds = append(s.LastCmds, lastCmd)

		// Stream logs in real time for each process
		go func(out string, stdout io.ReadCloser, stderr io.ReadCloser) {
			stdoutReader := bufio.NewReader(stdout)
			for {
				line, err := stdoutReader.ReadString('\n')
				line = strings.TrimRight(line, "\r\n")
				if len(line) > 0 {
					s.mu.Lock()
					s.appendLog("[" + out + "] " + line)
					s.mu.Unlock()
				}
				if err != nil {
					if err == io.EOF {
						break
					}
				}
			}
		}(outURL, stdoutPipe, stderrPipe)
		go func(out string, stderr io.ReadCloser) {
			stderrReader := bufio.NewReader(stderr)
			for {
				line, err := stderrReader.ReadString('\n')
				line = strings.TrimRight(line, "\r\n")
				if len(line) > 0 {
					s.mu.Lock()
					s.appendLog("[ERR][" + out + "] " + line)
					s.mu.Unlock()
				}
				if err != nil {
					if err == io.EOF {
						break
					}
				}
			}
		}(outURL, stderrPipe)
		go func(cmd *exec.Cmd, url string) {
			cmd.Wait()
			s.mu.Lock()
			// Only mark inactive if all processes have exited
			allExited := true
			for _, c := range s.cmds {
				if c != nil && c.ProcessState != nil && !c.ProcessState.Exited() {
					allExited = false
					break
				}
			}
			if allExited {
				s.Active = false
			}
			s.mu.Unlock()
		}(cmd, outURL)
	}

	s.Active = true
	s.RelayConfig = cfg
	return nil
}

// UpdateRelay updates the output URLs for the running relay without stopping the input stream.
// It starts new ffmpeg processes for new outputs and stops processes for removed outputs.
func (s *StreamManager) UpdateRelay(newCfg RTMPRelayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Active {
		return s.StartRelay(newCfg)
	}
	// Find outputs to add and remove
	current := make(map[string]bool)
	for _, url := range s.RelayConfig.OutputURLs {
		current[url] = true
	}
	newSet := make(map[string]bool)
	for _, url := range newCfg.OutputURLs {
		newSet[url] = true
	}
	// Stop removed outputs
	for i, url := range s.RelayConfig.OutputURLs {
		if !newSet[url] && s.cmds[i] != nil && s.cmds[i].Process != nil {
			s.cmds[i].Process.Kill()
			s.appendLog("[INFO] Stopped relay to removed output: " + url)
		}
	}
	// Remove stopped cmds and LastCmds
	var newCmds []*exec.Cmd
	var newLastCmds []string
	var keepOutputs []string
	for i, url := range s.RelayConfig.OutputURLs {
		if newSet[url] {
			newCmds = append(newCmds, s.cmds[i])
			newLastCmds = append(newLastCmds, s.LastCmds[i])
			keepOutputs = append(keepOutputs, url)
		}
	}
	// Add new outputs
	for _, url := range newCfg.OutputURLs {
		if !current[url] {
			cmd := buildFFmpegCmd(newCfg.InputURL, url)
			lastCmd := fmt.Sprintf("ffmpeg -re -i %s -c copy -f flv %s", newCfg.InputURL, url)
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
			newCmds = append(newCmds, cmd)
			newLastCmds = append(newLastCmds, lastCmd)
			keepOutputs = append(keepOutputs, url)
			go func(out string, stdout io.ReadCloser, stderr io.ReadCloser) {
				stdoutReader := bufio.NewReader(stdout)
				for {
					line, err := stdoutReader.ReadString('\n')
					line = strings.TrimRight(line, "\r\n")
					if len(line) > 0 {
						s.mu.Lock()
						s.appendLog("[" + out + "] " + line)
						s.mu.Unlock()
					}
					if err != nil {
						if err == io.EOF {
							break
						}
					}
				}
			}(url, stdoutPipe, stderrPipe)
			go func(out string, stderr io.ReadCloser) {
				stderrReader := bufio.NewReader(stderr)
				for {
					line, err := stderrReader.ReadString('\n')
					line = strings.TrimRight(line, "\r\n")
					if len(line) > 0 {
						s.mu.Lock()
						s.appendLog("[ERR][" + out + "] " + line)
						s.mu.Unlock()
					}
					if err != nil {
						if err == io.EOF {
							break
						}
					}
				}
			}(url, stderrPipe)
			go func(cmd *exec.Cmd, url string) {
				cmd.Wait()
				s.mu.Lock()
				// Only mark inactive if all processes have exited
				allExited := true
				for _, c := range s.cmds {
					if c != nil && c.ProcessState != nil && !c.ProcessState.Exited() {
						allExited = false
						break
					}
				}
				if allExited {
					s.Active = false
				}
				s.mu.Unlock()
			}(cmd, url)
			s.appendLog("[INFO] Started relay to new output: " + url)
		}
	}
	// Update state
	s.cmds = newCmds
	s.LastCmds = newLastCmds
	s.RelayConfig.OutputURLs = keepOutputs
	// Set Active true if any process is running
	for _, cmd := range s.cmds {
		if cmd != nil && cmd.Process != nil && (cmd.ProcessState == nil || !cmd.ProcessState.Exited()) {
			s.Active = true
			return nil
		}
	}
	s.Active = false
	return nil
}

func (s *StreamManager) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Active || len(s.cmds) == 0 {
		return nil // Not running
	}
	for _, cmd := range s.cmds {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	s.Active = false
	s.cmds = nil
	return nil
}

func (s *StreamManager) Status() (bool, RTMPRelayConfig, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Active, s.RelayConfig, s.LastCmds
}

func (s *StreamManager) GetLogs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logLines...)
}
