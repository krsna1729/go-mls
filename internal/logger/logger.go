package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
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
	mu     sync.Mutex
	logger *log.Logger
}

func NewLogger() *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{
		level:  lvl,
		logger: log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile),
	}
}

func NewLoggerWithWriter(w io.Writer) *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{
		level:  lvl,
		logger: log.New(w, "", log.LstdFlags|log.Lshortfile),
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
		level:  lvl,
		logger: log.New(w, "", log.LstdFlags|log.Lshortfile),
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
	l.mu.Lock()
	defer l.mu.Unlock()
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
