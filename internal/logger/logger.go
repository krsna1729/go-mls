package logger

import (
	"log"
	"os"
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
	level LogLevel
}

func NewLogger() *Logger {
	lvl := INFO
	if os.Getenv("GO_MLS_DEBUG") == "1" {
		lvl = DEBUG
	}
	return &Logger{level: lvl}
}

func (l *Logger) Debug(msg string, args ...interface{}) {
	if l.level <= DEBUG {
		log.Printf("[DEBUG] "+msg, args...)
	}
}
func (l *Logger) Info(msg string, args ...interface{}) {
	if l.level <= INFO {
		log.Printf("[INFO] "+msg, args...)
	}
}
func (l *Logger) Warn(msg string, args ...interface{}) {
	if l.level <= WARN {
		log.Printf("[WARN] "+msg, args...)
	}
}
func (l *Logger) Error(msg string, args ...interface{}) {
	if l.level <= ERROR {
		log.Printf("[ERROR] "+msg, args...)
	}
}
func (l *Logger) Fatal(msg string, args ...interface{}) {
	if l.level <= FATAL {
		log.Printf("[FATAL] "+msg, args...)
		os.Exit(1)
	}
}
