package logger

import (
	"io"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type Logger struct {
	slog         *slog.Logger
	level        slog.Level
	warnMu       sync.Map // for rate-limited logging (optional)
	errMu        sync.Map
	warnInterval time.Duration
	errInterval  time.Duration
}

// NewLogger returns a default logger (stderr, Info level, text format)
func NewLogger() *Logger {
	return NewLoggerWithConfig("info", "")
}

// NewLoggerWithConfig returns a logger with the given level and file (text format)
func NewLoggerWithConfig(levelStr, file string) *Logger {
	lvl := parseLogLevel(levelStr)
	var w io.Writer = os.Stderr
	if file != "" {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			w = f
		}
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	return &Logger{
		slog:         slog.New(h),
		level:        lvl,
		warnInterval: 1 * time.Second,
		errInterval:  1 * time.Second,
	}
}

func parseLogLevel(levelStr string) slog.Level {
	switch levelStr {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// With adds fields to the logger (e.g., component, function)
func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		slog:         l.slog.With(args...),
		level:        l.level,
		warnInterval: l.warnInterval,
		errInterval:  l.errInterval,
	}
}

func (l *Logger) Debug(msg string, args ...any) {
	l.slog.Debug(msg, args...)
}
func (l *Logger) Info(msg string, args ...any) {
	l.slog.Info(msg, args...)
}
func (l *Logger) Warn(msg string, args ...any) {
	l.slog.Warn(msg, args...)
}
func (l *Logger) Error(msg string, args ...any) {
	l.slog.Error(msg, args...)
}
func (l *Logger) Fatal(msg string, args ...any) {
	l.slog.Error(msg, args...)
	os.Exit(1)
}

// WarnRateLimited logs a warning at most once per interval for each unique callsite+message
func (l *Logger) WarnRateLimited(msg string, args ...any) {
	if l.level > slog.LevelWarn {
		return
	}
	key := l.rateLimitKey(msg)
	now := time.Now()
	shouldLog := false
	lastAny, ok := l.warnMu.Load(key)
	if !ok || now.Sub(lastAny.(time.Time)) > l.warnInterval {
		l.warnMu.Store(key, now)
		shouldLog = true
	}
	if shouldLog {
		l.slog.Warn(msg, args...)
	}
}

// ErrorRateLimited logs an error at most once per interval for each unique callsite+message
func (l *Logger) ErrorRateLimited(msg string, args ...any) {
	if l.level > slog.LevelError {
		return
	}
	key := l.rateLimitKey(msg)
	now := time.Now()
	shouldLog := false
	lastAny, ok := l.errMu.Load(key)
	if !ok || now.Sub(lastAny.(time.Time)) > l.errInterval {
		l.errMu.Store(key, now)
		shouldLog = true
	}
	if shouldLog {
		l.slog.Error(msg, args...)
	}
}

// rateLimitKey generates a key based on callsite and message
func (l *Logger) rateLimitKey(msg string) string {
	// runtime.Caller(2) skips rate-limited method and public log method
	// Use runtime.Caller to get file:line
	// Note: import "runtime"
	_, file, line, ok := runtime.Caller(2)
	if ok {
		return file + ":" + strconv.Itoa(line) + ":" + msg
	}
	return msg // fallback if runtime not available
}
