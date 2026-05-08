package reacttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"forge/internal/react"
)

func TestSpawnAgentToolReturnsRunningEnvelope(t *testing.T) {
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "ok", nil
	})
	tool := NewSpawnAgent(pool)

	raw, err := tool.Execute(context.Background(), map[string]any{
		"task_description": "inspect repo",
		"role":             "explorer",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != string(react.AgentStatusRunning) {
		t.Fatalf("status = %#v", payload["status"])
	}
	if payload["id"] == "" {
		t.Fatalf("id = %#v", payload["id"])
	}
}

func TestSpawnAgentToolAdvertisesDefaultAgents(t *testing.T) {
	pool := react.NewAgentPool(nil)
	pool.RegisterAgents(react.DefaultAgentDefinitions())
	tool := NewSpawnAgent(pool)

	for _, want := range []string{"repo-auditor", "code-reviewer", "oracle"} {
		if !strings.Contains(tool.Description, want) {
			t.Fatalf("spawn_agent description missing %q: %s", want, tool.Description)
		}
	}
}

func TestSpawnAgentSanitizesWriteTasksForReadOnlyAgents(t *testing.T) {
	cases := []string{
		"Audit the repo and create docs/superpowers/audits/2026-05-07-forge-plan-followup-audit.md",
		"Perform the audit. File creation target: docs/reports/2026-05-07-best-of-claude-plan-followup-audit.md",
		"Write a new report at /Users/cass/git/forge/docs/reports/2026-05-07-best-of-claude-plan-followup-audit.md",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			seenTask := make(chan string, 1)
			pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
				seenTask <- task
				return "unexpected", nil
			})
			pool.RegisterAgents(react.DefaultAgentDefinitions())
			tool := NewSpawnAgent(pool)

			if _, err := tool.Execute(context.Background(), map[string]any{
				"task_description": task,
				"role":             "repo-auditor",
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-seenTask:
				for _, want := range []string{"Inspect/analyze only", "parent agent can save", "Original delegated context", task} {
					if !strings.Contains(got, want) {
						t.Fatalf("sanitized task missing %q:\n%s", want, got)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("spawn function was not called")
			}
		})
	}
}

func TestWaitAgentToolReturnsCompletionEnvelope(t *testing.T) {
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "result text", nil
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{
		"task_description": "inspect repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	if id == "" {
		t.Fatal("spawn id missing")
	}

	rawWait, err := wait.Execute(context.Background(), map[string]any{
		"id":              id,
		"timeout_seconds": 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var waitPayload map[string]any
	if err := json.Unmarshal([]byte(rawWait), &waitPayload); err != nil {
		t.Fatal(err)
	}
	if waitPayload["status"] != string(react.AgentStatusCompleted) {
		t.Fatalf("status = %#v", waitPayload["status"])
	}
	if waitPayload["result"] != "result text" {
		t.Fatalf("result = %#v", waitPayload["result"])
	}
}
