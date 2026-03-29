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
