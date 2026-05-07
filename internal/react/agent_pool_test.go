package react

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAgentPoolSpawnAndWaitComplete(t *testing.T) {
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return role + ":" + task, nil
	})

	id, err := pool.Spawn(context.Background(), "explorer", "inspect repo")
	if err != nil {
		t.Fatal(err)
	}
	result, err := pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusCompleted {
		t.Fatalf("status = %q, want %q", result.Status, AgentStatusCompleted)
	}
	if result.Result != "explorer:inspect repo" {
		t.Fatalf("result = %q", result.Result)
	}
}

func TestAgentPoolWaitTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(started)
		<-release
		return "done", nil
	})

	id, err := pool.Spawn(context.Background(), "worker", "change file")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	result, err := pool.Wait(context.Background(), id, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusTimeout {
		t.Fatalf("status = %q, want %q", result.Status, AgentStatusTimeout)
	}
	close(release)
}

func TestAgentPoolWaitFailed(t *testing.T) {
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "", errors.New("boom")
	})
	id, err := pool.Spawn(context.Background(), "worker", "change file")
	if err != nil {
		t.Fatal(err)
	}
	result, err := pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, AgentStatusFailed)
	}
	if result.Error == "" {
		t.Fatal("expected error text")
	}
}

func TestMapSpawnRole(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "default", want: "default"},
		{in: "explorer", want: "explorer"},
		{in: "worker", want: "worker"},
		{in: "  explorer  ", want: "explorer"},
		{in: "unknown", want: "unknown"},
		{in: "qa-review", want: "qa-review"},
		{in: "", want: "default"},
	}
	for _, tc := range tests {
		if got := MapSpawnRole(tc.in); got != tc.want {
			t.Fatalf("MapSpawnRole(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultAgentDefinitionsIncludeNativeSpecialists(t *testing.T) {
	defs := DefaultAgentDefinitions()
	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Name] = true
		if def.SystemPrompt == "" {
			t.Fatalf("default agent %q missing system prompt", def.Name)
		}
	}

	for _, want := range []string{"repo-auditor", "code-reviewer", "explorer", "oracle", "synthesizer"} {
		if !names[want] {
			t.Fatalf("default agents missing %q in %#v", want, names)
		}
	}
}

func TestAgentPoolMatchesRegisteredAgentsWithSpacesOrHyphens(t *testing.T) {
	pool := NewAgentPool(nil)
	pool.RegisterAgents([]AgentDefinition{{Name: "repo-auditor", SystemPrompt: "audit"}})

	for _, role := range []string{"repo-auditor", "repo auditor", "Repo Auditor", "repo_auditor"} {
		agent, ok := pool.GetAgent(role)
		if !ok {
			t.Fatalf("GetAgent(%q) not found", role)
		}
		if agent.SystemPrompt != "audit" {
			t.Fatalf("GetAgent(%q) = %#v", role, agent)
		}
	}
}
