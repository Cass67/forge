package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safeTopicRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeTopic(topic string) string {
	s := strings.TrimSpace(topic)
	if strings.HasSuffix(strings.ToLower(s), ".md") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	s = safeTopicRe.ReplaceAllString(s, "-")
	if s == "" {
		s = "scratch"
	}
	return s
}

// NewScratchpadWrite creates a tool for writing to the shared scratchpad.
func NewScratchpadWrite(workDir string) Tool {
	var lastDiff string
	return Tool{
		Name:        "scratchpad_write",
		Description: "Write findings to the shared scratchpad (.forge/scratchpad/) for context across agent delegations.",
		Parameters: []ParameterDef{
			{Name: "topic", Type: "string", Description: "Topic name (becomes filename)", Required: true},
			{Name: "content", Type: "string", Description: "Content to write", Required: true},
		},
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			diff := lastDiff
			lastDiff = ""
			return diff
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lastDiff = ""
			topic, _ := args["topic"].(string)
			content, _ := args["content"].(string)
			if topic == "" || content == "" {
				return "", fmt.Errorf("topic and content are required")
			}
			dir := filepath.Join(workDir, ".forge", "scratchpad")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("create scratchpad dir: %w", err)
			}
			path := filepath.Join(dir, sanitizeTopic(topic)+".md")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", fmt.Errorf("write scratchpad: %w", err)
			}
			lastDiff = fmt.Sprintf("scratchpad_write: %s", path)
			return fmt.Sprintf("written to %s", path), nil
		},
	}
}

// NewScratchpadRead creates a tool for reading from the shared scratchpad.
func NewScratchpadRead(workDir string) Tool {
	return Tool{
		Name:        "scratchpad_read",
		Description: "Read findings from the shared scratchpad (.forge/scratchpad/).",
		Parameters: []ParameterDef{
			{Name: "topic", Type: "string", Description: "Topic name to read", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			topic, _ := args["topic"].(string)
			if topic == "" {
				return "", fmt.Errorf("topic is required")
			}
			path := filepath.Join(workDir, ".forge", "scratchpad", sanitizeTopic(topic)+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("scratchpad topic %q not found", topic)
			}
			return string(data), nil
		},
	}
}
