package process

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProcUsage holds CPU and memory usage info
// CPU is percent, Mem is bytes
// Cmdline is for debugging

type ProcUsage struct {
	PID     int     `json:"pid"`
	CPU     float64 `json:"cpu"`
	Mem     uint64  `json:"mem"`
	Cmdline string  `json:"cmdline,omitempty"`
}

// GetSelfUsage returns usage for the current process
func GetSelfUsage() (*ProcUsage, error) {
	pid := os.Getpid()
	return GetProcUsage(pid)
}

// GetProcUsage returns usage for a given pid
func GetProcUsage(pid int) (*ProcUsage, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statmPath := fmt.Sprintf("/proc/%d/statm", pid)
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)

	// Check if the process still exists by trying to read its stat file
	stat, err := os.ReadFile(statPath)
	if err != nil {
		return nil, fmt.Errorf("process %d not found or inaccessible: %w", pid, err)
	}

	// Ensure we have valid stat data
	if len(stat) == 0 {
		return nil, fmt.Errorf("process %d stat file is empty", pid)
	}

	statm, err := os.ReadFile(statmPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read statm file for process %d: %w", pid, err)
	}
	cmdline, _ := os.ReadFile(cmdlinePath)

	fields := strings.Fields(string(stat))
	if len(fields) < 24 {
		return nil, fmt.Errorf("unexpected stat fields for process %d: got %d, need at least 24", pid, len(fields))
	}

	// Parse CPU times safely
	utime, err := strconv.ParseFloat(fields[13], 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse utime for process %d: %w", pid, err)
	}
	stime, err := strconv.ParseFloat(fields[14], 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse stime for process %d: %w", pid, err)
	}
	cutime, err := strconv.ParseFloat(fields[15], 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cutime for process %d: %w", pid, err)
	}
	cstime, err := strconv.ParseFloat(fields[16], 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cstime for process %d: %w", pid, err)
	}
	totalTime := utime + stime + cutime + cstime

	// Handle uptime reading with robust error checking
	uptimeBytes, err := os.ReadFile("/proc/uptime")
	if err != nil {
		// During shutdown, /proc/uptime might be inaccessible
		uptimeBytes = []byte("0 0")
	}
	uptimeFields := strings.Fields(string(uptimeBytes))
	uptime := 0.0
	if len(uptimeFields) > 0 {
		parsed, err := strconv.ParseFloat(uptimeFields[0], 64)
		if err == nil {
			uptime = parsed
		}
		// If parsing fails, uptime remains 0.0
	}

	starttime, err := strconv.ParseFloat(fields[21], 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse starttime for process %d: %w", pid, err)
	}
	clkTck := float64(100) // Linux default
	seconds := uptime - (starttime / clkTck)
	cpuPercent := 0.0
	if seconds > 0 {
		cpuPercent = 100 * (totalTime / clkTck) / seconds
	}

	memFields := strings.Fields(string(statm))
	mem := uint64(0)
	if len(memFields) > 1 {
		pages, err := strconv.ParseUint(memFields[1], 10, 64)
		if err == nil {
			mem = pages * 4096 // page size
		}
		// If parsing fails, mem remains 0
	}

	return &ProcUsage{
		PID:     pid,
		CPU:     cpuPercent,
		Mem:     mem,
		Cmdline: strings.ReplaceAll(string(cmdline), "\x00", " "),
	}, nil
}

// GetChildrenUsage returns usage for all child processes of this process
func GetChildrenUsage() ([]*ProcUsage, error) {
	self := os.Getpid()
	procs, _ := os.ReadDir("/proc")
	var children []*ProcUsage
	for _, p := range procs {
		if !p.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		statPath := fmt.Sprintf("/proc/%d/stat", pid)
		stat, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(stat))
		if len(fields) < 4 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[3])
		if ppid == self {
			u, err := GetProcUsage(pid)
			if err == nil {
				children = append(children, u)
			}
		}
	}
	return children, nil
}
