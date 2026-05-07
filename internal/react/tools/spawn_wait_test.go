package reacttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
