package ffmpeg

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

var (
	ErrProcessFailed = errors.New("process failed")
	ErrProcessKilled = errors.New("process killed")
)

func IsProcessKilled(err error) bool {
	if err == nil {
		return false
	}
	// Check for wrapped error
	if errors.Is(err, ErrProcessKilled) {
		return true
	}
	// Check for raw signal: killed error from exec.Cmd.Wait()
	if err.Error() == "signal: killed" {
		return true
	}
	// Check for signaled exit
	if IsSignaled(err) {
		return true
	}
	return false
}

func IsProcessFailed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrProcessFailed) {
		return true
	}
	// Check for other non-zero exit codes
	if IsExitError(err) {
		return GetExitCode(err) != 0
	}
	return false
}

func IsExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func GetExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func IsSignaled(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		if ok {
			return ws.Signaled()
		}
	}
	return false
}

func GetSignal(err error) syscall.Signal {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		if ok {
			return ws.Signal()
		}
	}
	return 0
}

func IsError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, os.ErrClosed)
}

func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	if IsProcessKilled(err) {
		return ErrProcessKilled
	}
	if IsProcessFailed(err) {
		return fmt.Errorf("%w: %v", ErrProcessFailed, err)
	}
	return err
}

func ErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func HasErrorSuffix(err error, suffix string) bool {
	return strings.HasSuffix(ErrorString(err), suffix)
}
