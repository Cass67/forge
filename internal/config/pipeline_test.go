package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePipelineBuiltinNames(t *testing.T) {
	builtins := map[string]BuiltinPrompt{
		"correctness": {Writer: "correctness-writer", Auditor: "correctness-auditor"},
		"security":    {Writer: "security-writer", Auditor: "security-auditor"},
	}
	pipeline := []PipelinePass{
		{Name: "correctness", Rounds: 2},
		{Name: "security"},
	}
	resolved, err := ResolvePipeline(pipeline, builtins, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Names) != 2 {
		t.Fatalf("expected 2 passes, got %d", len(resolved.Names))
	}
	if resolved.Rounds[0] != 2 {
		t.Errorf("expected rounds=2, got %d", resolved.Rounds[0])
	}
	if resolved.Rounds[1] != 5 {
		t.Errorf("expected default rounds=5, got %d", resolved.Rounds[1])
	}
	if resolved.WriterPrompts[0] != "correctness-writer" {
		t.Errorf("expected built-in writer prompt")
	}
	if resolved.AuditorPrompts[1] != "security-auditor" {
		t.Errorf("expected built-in auditor prompt")
	}
}

func TestResolvePipelineCustomFiles(t *testing.T) {
	dir := t.TempDir()
	writerFile := filepath.Join(dir, "writer.md")
	auditorFile := filepath.Join(dir, "auditor.md")
	os.WriteFile(writerFile, []byte("custom writer prompt"), 0o644)
	os.WriteFile(auditorFile, []byte("custom auditor prompt"), 0o644)

	pipeline := []PipelinePass{
		{Name: "perf", WriterPrompt: writerFile, AuditorPrompt: auditorFile, Rounds: 3},
	}
	resolved, err := ResolvePipeline(pipeline, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.WriterPrompts[0] != "custom writer prompt" {
		t.Errorf("unexpected writer prompt: %q", resolved.WriterPrompts[0])
	}
	if resolved.AuditorPrompts[0] != "custom auditor prompt" {
		t.Errorf("unexpected auditor prompt: %q", resolved.AuditorPrompts[0])
	}
}

func TestResolvePipelineMissingName(t *testing.T) {
	pipeline := []PipelinePass{{}}
	_, err := ResolvePipeline(pipeline, nil, 5)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestResolvePipelineMissingFile(t *testing.T) {
	pipeline := []PipelinePass{
		{Name: "test", WriterPrompt: "/nonexistent/path.md"},
	}
	_, err := ResolvePipeline(pipeline, nil, 5)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestResolvePipelineUnknownBuiltin(t *testing.T) {
	pipeline := []PipelinePass{
		{Name: "unknown-pass"},
	}
	resolved, err := ResolvePipeline(pipeline, map[string]BuiltinPrompt{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Should get a generic fallback prompt
	if resolved.WriterPrompts[0] == "" {
		t.Error("expected non-empty fallback prompt")
	}
}
