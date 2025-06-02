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

	stat, err := os.ReadFile(statPath)
	if err != nil {
		return nil, err
	}
	statm, err := os.ReadFile(statmPath)
	if err != nil {
		return nil, err
	}
	cmdline, _ := os.ReadFile(cmdlinePath)

	fields := strings.Fields(string(stat))
	if len(fields) < 24 {
		return nil, fmt.Errorf("unexpected stat fields")
	}
	utime, _ := strconv.ParseFloat(fields[13], 64)
	stime, _ := strconv.ParseFloat(fields[14], 64)
	cutime, _ := strconv.ParseFloat(fields[15], 64)
	cstime, _ := strconv.ParseFloat(fields[16], 64)
	totalTime := utime + stime + cutime + cstime

	uptimeBytes, err := os.ReadFile("/proc/uptime")
	if err != nil {
		uptimeBytes = []byte("0 0")
	}
	uptimeFields := strings.Fields(string(uptimeBytes))
	uptime, _ := strconv.ParseFloat(uptimeFields[0], 64)

	starttime, _ := strconv.ParseFloat(fields[21], 64)
	clkTck := float64(100) // Linux default
	seconds := uptime - (starttime / clkTck)
	cpuPercent := 0.0
	if seconds > 0 {
		cpuPercent = 100 * (totalTime / clkTck) / seconds
	}

	memFields := strings.Fields(string(statm))
	mem := uint64(0)
	if len(memFields) > 1 {
		pages, _ := strconv.ParseUint(memFields[1], 10, 64)
		mem = pages * 4096 // page size
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
