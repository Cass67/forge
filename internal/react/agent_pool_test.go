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
		{in: "default", want: "builder"},
		{in: "explorer", want: "scout"},
		{in: "worker", want: "builder"},
		{in: "doctor", want: "doctor"},
		{in: "architect", want: "architect"},
		{in: "builder", want: "builder"},
		{in: "unknown", want: "builder"},
	}
	for _, tc := range tests {
		if got := MapSpawnRole(tc.in); got != tc.want {
			t.Fatalf("MapSpawnRole(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
