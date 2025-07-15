package logger

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

type Logger struct {
	level  LogLevel
	logger *log.Logger
	// For rate-limited logging: sync.Map[key] = last log time
	warnRateLimit  sync.Map // map[string]time.Time
	errorRateLimit sync.Map // map[string]time.Time
	warnInterval   time.Duration
	errorInterval  time.Duration
}

func NewLogger() *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{
		level:         lvl,
		logger:        log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile),
		warnInterval:  1 * time.Second,
		errorInterval: 1 * time.Second,
	}
}

func NewLoggerWithWriter(w io.Writer) *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{
		level:         lvl,
		logger:        log.New(w, "", log.LstdFlags|log.Lshortfile),
		warnInterval:  1 * time.Second,
		errorInterval: 1 * time.Second,
	}
}

// NewLoggerWithConfig creates a logger with the given level and file
func NewLoggerWithConfig(levelStr, file string) *Logger {
	lvl := parseLogLevel(levelStr)
	var w io.Writer = os.Stderr
	if file != "" {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			w = f
		}
	}
	return &Logger{
		level:         lvl,
		logger:        log.New(w, "", log.LstdFlags|log.Lshortfile),
		warnInterval:  1 * time.Second,
		errorInterval: 1 * time.Second,
	}
}

// NewLoggerWithRateLimit creates a logger with rate-limited warning/error logging
func NewLoggerWithRateLimit(w io.Writer, warnInterval, errorInterval time.Duration) *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{
		level:         lvl,
		logger:        log.New(w, "", log.LstdFlags|log.Lshortfile),
		warnInterval:  warnInterval,
		errorInterval: errorInterval,
	}
}

func parseLogLevel(levelStr string) LogLevel {
	switch strings.ToLower(levelStr) {
	case "debug":
		return DEBUG
	case "info":
		return INFO
	case "warn":
		return WARN
	case "error":
		return ERROR
	case "fatal":
		return FATAL
	default:
		return INFO
	}
}

func (l *Logger) logWithCaller(level string, msg string, args ...interface{}) {
	// No locking here: only printing, not mutating shared state
	// runtime.Caller(2) skips logWithCaller and the public log method
	_, file, line, ok := runtime.Caller(2)
	fileline := ""
	if ok {
		short := file
		if idx := strings.LastIndex(file, "/"); idx != -1 {
			short = file[idx+1:]
		}
		fileline = fmt.Sprintf("%s:%d: ", short, line)
	}
	l.logger.Printf("[%s] %s"+msg, append([]interface{}{level, fileline}, args...)...)
}

// WarnRateLimited logs a warning at most once per interval for each unique callsite+message
func (l *Logger) WarnRateLimited(msg string, args ...interface{}) {
	if l.level > WARN {
		return
	}
	key := l.rateLimitKey(msg)
	now := time.Now()
	shouldLog := false
	lastAny, ok := l.warnRateLimit.Load(key)
	if !ok || now.Sub(lastAny.(time.Time)) > l.warnInterval {
		l.warnRateLimit.Store(key, now)
		shouldLog = true
	}
	if shouldLog {
		l.logWithCaller("WARN", msg, args...)
	}
}

// ErrorRateLimited logs an error at most once per interval for each unique callsite+message
func (l *Logger) ErrorRateLimited(msg string, args ...interface{}) {
	if l.level > ERROR {
		return
	}
	key := l.rateLimitKey(msg)
	now := time.Now()
	shouldLog := false
	lastAny, ok := l.errorRateLimit.Load(key)
	if !ok || now.Sub(lastAny.(time.Time)) > l.errorInterval {
		l.errorRateLimit.Store(key, now)
		shouldLog = true
	}
	if shouldLog {
		l.logWithCaller("ERROR", msg, args...)
	}
}

// rateLimitKey generates a key based on callsite and message
func (l *Logger) rateLimitKey(msg string) string {
	// runtime.Caller(2) skips rate-limited method and public log method
	_, file, line, ok := runtime.Caller(3)
	if !ok {
		return msg
	}
	short := file
	if idx := strings.LastIndex(file, "/"); idx != -1 {
		short = file[idx+1:]
	}
	return fmt.Sprintf("%s:%d:%s", short, line, msg)
}

func (l *Logger) Debug(msg string, args ...interface{}) {
	if l.level <= DEBUG {
		l.logWithCaller("DEBUG", msg, args...)
	}
}
func (l *Logger) Info(msg string, args ...interface{}) {
	if l.level <= INFO {
		l.logWithCaller("INFO", msg, args...)
	}
}
func (l *Logger) Warn(msg string, args ...interface{}) {
	if l.level <= WARN {
		l.logWithCaller("WARN", msg, args...)
	}
}
func (l *Logger) Error(msg string, args ...interface{}) {
	if l.level <= ERROR {
		l.logWithCaller("ERROR", msg, args...)
	}
}
func (l *Logger) Fatal(msg string, args ...interface{}) {
	if l.level <= FATAL {
		l.logWithCaller("FATAL", msg, args...)
		os.Exit(1)
	}
}

func TestLogger_WarnRateLimited(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLoggerWithRateLimit(buf, 100*time.Millisecond, 100*time.Millisecond)
	logger.level = DEBUG // allow all logs

	for i := 0; i < 5; i++ {
		logger.WarnRateLimited("rate-limited warning: %d", 42)
	}
	first := buf.String()
	if strings.Count(first, "rate-limited warning") != 1 {
		t.Errorf("expected 1 warning in first burst, got %d", strings.Count(first, "rate-limited warning"))
	}

	time.Sleep(120 * time.Millisecond)
	logger.WarnRateLimited("rate-limited warning: %d", 42)
	second := buf.String()
	if strings.Count(second, "rate-limited warning") != 2 {
		t.Errorf("expected 2 warnings after interval, got %d", strings.Count(second, "rate-limited warning"))
	}
}

func TestLogger_ErrorRateLimited(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLoggerWithRateLimit(buf, 100*time.Millisecond, 100*time.Millisecond)
	logger.level = DEBUG // allow all logs

	for i := 0; i < 5; i++ {
		logger.ErrorRateLimited("rate-limited error: %d", 99)
	}
	first := buf.String()
	if strings.Count(first, "rate-limited error") != 1 {
		t.Errorf("expected 1 error in first burst, got %d", strings.Count(first, "rate-limited error"))
	}

	time.Sleep(120 * time.Millisecond)
	logger.ErrorRateLimited("rate-limited error: %d", 99)
	second := buf.String()
	if strings.Count(second, "rate-limited error") != 2 {
		t.Errorf("expected 2 errors after interval, got %d", strings.Count(second, "rate-limited error"))
	}
}
