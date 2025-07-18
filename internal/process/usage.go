package process

import (
	"fmt"
	"os"

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

// GetSelfUsage returns usage for the current process using gopsutil
func GetSelfUsage() (*ProcUsage, error) {
	pid := int32(os.Getpid())

	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to create process object: %w", err)
	}

	// Get CPU percentage (this automatically handles sampling internally)
	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		// If CPU percent fails, default to 0.0
		cpuPercent = 0.0
	}

	// Get memory info (RSS - Resident Set Size)
	memInfo, err := proc.MemoryInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info: %w", err)
	}

	// Get command line for debugging
	cmdline, _ := proc.Cmdline()

	return &ProcUsage{
		PID:     int(pid),
		CPU:     cpuPercent,
		Mem:     memInfo.RSS, // RSS matches what ps shows
		Cmdline: cmdline,
	}, nil
}

// GetProcUsage returns usage for a given pid using gopsutil
func GetProcUsage(pid int) (*ProcUsage, error) {
	// Special case: if it's our own PID, use GetSelfUsage for consistency
	if pid == os.Getpid() {
		return GetSelfUsage()
	}

	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, fmt.Errorf("process %d not found or inaccessible: %w", pid, err)
	}

	// Check if process exists
	exists, err := proc.IsRunning()
	if err != nil || !exists {
		return nil, fmt.Errorf("process %d is not running", pid)
	}

	// Get CPU percentage
	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		// If CPU percent fails, default to 0.0
		cpuPercent = 0.0
	}

	// Get memory info
	memInfo, err := proc.MemoryInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info for process %d: %w", pid, err)
	}

	// Get command line for debugging
	cmdline, _ := proc.Cmdline()

	return &ProcUsage{
		PID:     pid,
		CPU:     cpuPercent,
		Mem:     memInfo.RSS,
		Cmdline: cmdline,
	}, nil
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
