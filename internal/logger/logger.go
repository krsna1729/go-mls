package logger

import (
	"io"
	"log"
	"os"
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
		logger: log.New(os.Stderr, "", log.LstdFlags),
	}
}

func NewLoggerWithWriter(w io.Writer) *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{
		level:  lvl,
		logger: log.New(w, "", log.LstdFlags),
	}
}

func (l *Logger) Debug(msg string, args ...interface{}) {
	if l.level <= DEBUG {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.logger.Printf("[DEBUG] "+msg, args...)
	}
}
func (l *Logger) Info(msg string, args ...interface{}) {
	if l.level <= INFO {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.logger.Printf("[INFO] "+msg, args...)
	}
}
func (l *Logger) Warn(msg string, args ...interface{}) {
	if l.level <= WARN {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.logger.Printf("[WARN] "+msg, args...)
	}
}
func (l *Logger) Error(msg string, args ...interface{}) {
	if l.level <= ERROR {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.logger.Printf("[ERROR] "+msg, args...)
	}
}
func (l *Logger) Fatal(msg string, args ...interface{}) {
	if l.level <= FATAL {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.logger.Printf("[FATAL] "+msg, args...)
		os.Exit(1)
	}
}
