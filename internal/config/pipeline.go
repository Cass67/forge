package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuiltinPrompt maps a built-in pass name to its embedded writer and auditor prompts.
type BuiltinPrompt struct {
	Writer  string
	Auditor string
}

// ResolvedPipeline holds the resolved passes and their prompt text.
type ResolvedPipeline struct {
	Names          []string
	Rounds         []int
	WriterPrompts  []string
	AuditorPrompts []string
}

// ResolvePipeline resolves the custom pipeline config into prompt text.
// builtins maps pass names like "correctness" to their embedded prompt text.
// defaultRounds is used when a pipeline entry has Rounds == 0.
func ResolvePipeline(pipeline []PipelinePass, builtins map[string]BuiltinPrompt, defaultRounds int) (*ResolvedPipeline, error) {
	r := &ResolvedPipeline{}
	for _, p := range pipeline {
		name := p.Name
		if name == "" {
			return nil, fmt.Errorf("pipeline pass missing name")
		}

		rounds := p.Rounds
		if rounds <= 0 {
			rounds = defaultRounds
		}

		writerPrompt, err := resolvePromptText(p.WriterPrompt, name, builtins, true)
		if err != nil {
			return nil, fmt.Errorf("pipeline pass %q writer_prompt: %w", name, err)
		}

		auditorPrompt, err := resolvePromptText(p.AuditorPrompt, name, builtins, false)
		if err != nil {
			return nil, fmt.Errorf("pipeline pass %q auditor_prompt: %w", name, err)
		}

		r.Names = append(r.Names, name)
		r.Rounds = append(r.Rounds, rounds)
		r.WriterPrompts = append(r.WriterPrompts, writerPrompt)
		r.AuditorPrompts = append(r.AuditorPrompts, auditorPrompt)
	}
	return r, nil
}

func resolvePromptText(path, name string, builtins map[string]BuiltinPrompt, isWriter bool) (string, error) {
	if path == "" {
		if bp, ok := builtins[name]; ok {
			if isWriter {
				return bp.Writer, nil
			}
			return bp.Auditor, nil
		}
		return fmt.Sprintf("You are working on the %s pass.", name), nil
	}

	expanded := expandPath(path)
	data, err := os.ReadFile(expanded)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", expanded, err)
	}
	return string(data), nil
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
