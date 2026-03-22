package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelInfo)

	l.Debug("should be hidden")
	if buf.Len() != 0 {
		t.Fatal("debug message should be filtered at info level")
	}

	l.Info("visible")
	if buf.Len() == 0 {
		t.Fatal("info message should not be filtered at info level")
	}
}

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug)

	l.Info("hello", map[string]any{"key": "val"})

	var entry Entry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.Level != LevelInfo {
		t.Errorf("level: got %q, want %q", entry.Level, LevelInfo)
	}
	if entry.Msg != "hello" {
		t.Errorf("msg: got %q, want %q", entry.Msg, "hello")
	}
	if entry.Fields["key"] != "val" {
		t.Errorf("fields[key]: got %v, want %q", entry.Fields["key"], "val")
	}
	if entry.Time == "" {
		t.Error("time should be set")
	}
}

func TestWith(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug).With(map[string]any{"session": "abc"})

	l.Info("test", map[string]any{"round": 1})

	var entry Entry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.Fields["session"] != "abc" {
		t.Errorf("expected session field from With()")
	}
	if entry.Fields["round"] != float64(1) {
		t.Errorf("expected round field from call-site, got %v", entry.Fields["round"])
	}
}

func TestNopDiscardsOutput(t *testing.T) {
	l := Nop()
	l.Error("something bad")
	// Nop writes to io.Discard, so no way to check output — just verify no panic.
}

func TestConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Info("concurrent")
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 100 {
		t.Fatalf("expected 100 log lines, got %d", len(lines))
	}
	for i, line := range lines {
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestAllLevelsAtDebug(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug)

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
}

func TestErrorLevelFiltersAll(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelError)

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	if buf.Len() != 0 {
		t.Fatal("debug/info/warn should be filtered at error level")
	}

	l.Error("e")
	if buf.Len() == 0 {
		t.Fatal("error should not be filtered at error level")
	}
}
