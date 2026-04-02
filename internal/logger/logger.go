package logger

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents a log severity.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Entry is a single structured log record.
type Entry struct {
	Time   string         `json:"time"`
	Level  Level          `json:"level"`
	Msg    string         `json:"msg"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Logger writes structured JSON log lines.
type Logger struct {
	mu       sync.Mutex
	out      io.Writer
	minLevel Level
	fields   map[string]any
}

// New creates a Logger that writes to out, filtering below minLevel.
func New(out io.Writer, level Level) *Logger {
	return &Logger{
		out:      out,
		minLevel: level,
		fields:   make(map[string]any),
	}
}

// NewFileLogger creates a logger that appends to a file, creating it if needed.
func NewFileLogger(path string, level Level) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return New(f, level), nil
}

// Nop returns a logger that discards all output.
func Nop() *Logger {
	return New(io.Discard, LevelError)
}

// With returns a new Logger that carries the given fields in every entry.
func (l *Logger) With(fields map[string]any) *Logger {
	merged := make(map[string]any, len(l.fields)+len(fields))
	for k, v := range l.fields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	return &Logger{
		out:      l.out,
		minLevel: l.minLevel,
		fields:   merged,
	}
}

func (l *Logger) Debug(msg string, fields ...map[string]any) { l.log(LevelDebug, msg, fields...) }
func (l *Logger) Info(msg string, fields ...map[string]any)  { l.log(LevelInfo, msg, fields...) }
func (l *Logger) Warn(msg string, fields ...map[string]any)  { l.log(LevelWarn, msg, fields...) }
func (l *Logger) Error(msg string, fields ...map[string]any) { l.log(LevelError, msg, fields...) }

var levelOrder = map[Level]int{
	LevelDebug: 0,
	LevelInfo:  1,
	LevelWarn:  2,
	LevelError: 3,
}

func (l *Logger) log(level Level, msg string, extraFields ...map[string]any) {
	if levelOrder[level] < levelOrder[l.minLevel] {
		return
	}

	entry := Entry{
		Time:  time.Now().UTC().Format(time.RFC3339),
		Level: level,
		Msg:   msg,
	}

	if len(l.fields) > 0 || len(extraFields) > 0 {
		merged := make(map[string]any, len(l.fields))
		for k, v := range l.fields {
			merged[k] = v
		}
		for _, ef := range extraFields {
			for k, v := range ef {
				merged[k] = v
			}
		}
		if len(merged) > 0 {
			entry.Fields = merged
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	l.mu.Lock()
	l.out.Write(data)
	l.mu.Unlock()
}
