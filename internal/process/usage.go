package process

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/process"
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

// Simple in-memory cache to avoid expensive gopsutil calls being made repeatedly.
var (
	usageCacheMu sync.Mutex
	usageCache   = make(map[int]*cachedUsage)
)

type cachedUsage struct {
	u  *ProcUsage
	ts time.Time
}

const usageCacheTTL = 500 * time.Millisecond

// GetSelfUsage returns usage for the current process using gopsutil
func GetSelfUsage() (*ProcUsage, error) {
	pid := os.Getpid()

	// Check cache first
	usageCacheMu.Lock()
	if cu, ok := usageCache[pid]; ok && time.Since(cu.ts) < usageCacheTTL {
		u := cu.u
		usageCacheMu.Unlock()
		return u, nil
	}
	usageCacheMu.Unlock()

	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, fmt.Errorf("failed to create process object: %w", err)
	}

	cpuPercent, err := p.CPUPercent()
	if err != nil {
		cpuPercent = 0.0
	}
	memInfo, err := p.MemoryInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info: %w", err)
	}
	cmdline, _ := p.Cmdline()

	u := &ProcUsage{
		PID:     pid,
		CPU:     cpuPercent,
		Mem:     memInfo.RSS,
		Cmdline: cmdline,
	}

	usageCacheMu.Lock()
	usageCache[pid] = &cachedUsage{u: u, ts: time.Now()}
	usageCacheMu.Unlock()

	return u, nil
}

// GetProcUsage returns usage for a given pid using gopsutil
func GetProcUsage(pid int) (*ProcUsage, error) {
	if pid == os.Getpid() {
		return GetSelfUsage()
	}

	// Cached fast-path
	usageCacheMu.Lock()
	if cu, ok := usageCache[pid]; ok && time.Since(cu.ts) < usageCacheTTL {
		u := cu.u
		usageCacheMu.Unlock()
		return u, nil
	}
	usageCacheMu.Unlock()

	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, fmt.Errorf("process %d not found or inaccessible: %w", pid, err)
	}

	exists, err := proc.IsRunning()
	if err != nil || !exists {
		return nil, fmt.Errorf("process %d is not running", pid)
	}

	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		cpuPercent = 0.0
	}

	memInfo, err := proc.MemoryInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info for process %d: %w", pid, err)
	}

	cmdline, _ := proc.Cmdline()

	u := &ProcUsage{
		PID:     pid,
		CPU:     cpuPercent,
		Mem:     memInfo.RSS,
		Cmdline: cmdline,
	}

	usageCacheMu.Lock()
	usageCache[pid] = &cachedUsage{u: u, ts: time.Now()}
	usageCacheMu.Unlock()

	return u, nil
}

// GetChildrenUsage returns usage for all child processes of this process
func GetChildrenUsage() ([]*ProcUsage, error) {
	selfPID := int32(os.Getpid())

	selfProc, err := process.NewProcess(selfPID)
	if err != nil {
		return nil, fmt.Errorf("failed to create self process object: %w", err)
	}

	// Get all child processes
	children, err := selfProc.Children()
	if err != nil {
		// If children() fails, return empty slice (not an error)
		return []*ProcUsage{}, nil
	}

	var childUsages []*ProcUsage
	for _, childProc := range children {
		childPID := int(childProc.Pid)
		usage, err := GetProcUsage(childPID)
		if err == nil {
			childUsages = append(childUsages, usage)
		}
		// Ignore errors for individual children (they might have exited)
	}

	return childUsages, nil
}
