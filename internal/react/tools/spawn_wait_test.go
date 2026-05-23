package reacttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
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

	for _, want := range []string{"repo-auditor", "code-reviewer", "oracle", "forge_handoff", "parent/orchestrator", "must not commit or push"} {
		if !strings.Contains(tool.Description, want) {
			t.Fatalf("spawn_agent description missing %q: %s", want, tool.Description)
		}
	}
}

func TestWaitAgentTimeoutDescriptionSaysCurrentStatus(t *testing.T) {
	tool := NewWaitAgent(nil)
	for _, param := range tool.Parameters {
		if param.Name != "timeout_seconds" {
			continue
		}
		if !strings.Contains(strings.ToLower(param.Description), "current status") {
			t.Fatalf("timeout_seconds description = %q, want current status wording", param.Description)
		}
		return
	}
	t.Fatal("timeout_seconds parameter not found")
}

func TestWaitAgentToolTimeoutExceedsDefaultToolTimeout(t *testing.T) {
	tool := NewWaitAgent(nil)
	if tool.Timeout <= agenttools.DefaultToolTimeout {
		t.Fatalf("wait_agent timeout = %v, want greater than default tool timeout %v", tool.Timeout, agenttools.DefaultToolTimeout)
	}
}

func TestWaitAgentReturnsRunningStatusWhenContextDeadlineWins(t *testing.T) {
	pool := react.NewAgentPool(func(ctx context.Context, _, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	id, err := pool.Spawn(context.Background(), "explorer", "keep running")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	wait := NewWaitAgent(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	raw, err := wait.Execute(ctx, map[string]any{"id": id, "timeout_seconds": 30.0})
	if err != nil {
		t.Fatalf("wait_agent returned error: %v", err)
	}
	var result react.AgentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", raw, err)
	}
	if result.Status != react.AgentStatusRunning {
		t.Fatalf("status = %q, want running (raw=%s)", result.Status, raw)
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
				for _, want := range []string{"Inspect/analyze only", "parent agent can save", "forge_handoff", "parent/orchestrator", "Original delegated context", task} {
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

func TestSpawnAgentWorkDirPassesThroughContext(t *testing.T) {
	ctx := context.Background()
	wantDir := "/some/target/dir"
	gotCtx := make(chan context.Context, 1)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		gotCtx <- ctx
		return "ok", nil
	})
	tool := NewSpawnAgent(pool)

	_, err := tool.Execute(ctx, map[string]any{
		"task_description": "inspect target",
		"work_dir":         wantDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ctxv := <-gotCtx:
		if got := react.WorkDirFromContext(ctxv); got != wantDir {
			t.Fatalf("WorkDirFromContext = %q, want %q", got, wantDir)
		}
	case <-time.After(time.Second):
		t.Fatal("spawn function never called")
	}
}

func TestSpawnAgentDetachesChildFromParentToolCancellation(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.Background())
	started := make(chan struct{})
	childCanceled := make(chan struct{})
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(started)
		<-ctx.Done()
		close(childCanceled)
		return "", ctx.Err()
	})
	tool := NewSpawnAgent(pool)

	raw, err := tool.Execute(parentCtx, map[string]any{
		"task_description": "inspect repo",
		"work_dir":         "/some/target/dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	id, _ := payload["id"].(string)
	if id == "" {
		t.Fatal("spawn id missing")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("spawn function never started")
	}

	cancelParent()
	select {
	case <-childCanceled:
		t.Fatal("child agent was canceled by parent tool context cancellation")
	case <-time.After(50 * time.Millisecond):
	}
	if result, err := pool.Kill(context.Background(), id); err != nil {
		t.Fatal(err)
	} else if result.Status != react.AgentStatusKilled {
		t.Fatalf("kill status = %q, want %q", result.Status, react.AgentStatusKilled)
	}
	select {
	case <-childCanceled:
	case <-time.After(time.Second):
		t.Fatal("kill_agent did not cancel child agent")
	}
}

func TestSpawnAgentDoesNotStartChildWhenParentToolContextAlreadyCanceled(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	called := make(chan struct{})
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(called)
		return "unexpected", nil
	})
	tool := NewSpawnAgent(pool)

	if _, err := tool.Execute(parentCtx, map[string]any{"task_description": "inspect repo"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want %v", err, context.Canceled)
	}
	select {
	case <-called:
		t.Fatal("spawn function was called for already-canceled parent context")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSpawnAgentOmitsWorkDirWhenNotProvided(t *testing.T) {
	var gotCtx context.Context
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		gotCtx = ctx
		return "ok", nil
	})
	tool := NewSpawnAgent(pool)

	_, err := tool.Execute(context.Background(), map[string]any{
		"task_description": "inspect repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := react.WorkDirFromContext(gotCtx); got != "" {
		t.Fatalf("WorkDirFromContext = %q, want empty", got)
	}
}

func TestSpawnAgentInfersWorkDirFromAbsoluteDelegatedPath(t *testing.T) {
	wantDir := t.TempDir()
	gotCtx := make(chan context.Context, 1)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		gotCtx <- ctx
		return "ok", nil
	})
	tool := NewSpawnAgent(pool)

	_, err := tool.Execute(context.Background(), map[string]any{
		"task_description": "Inspect " + wantDir + " repository comprehensively and return findings.",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ctxv := <-gotCtx:
		if got := react.WorkDirFromContext(ctxv); got != wantDir {
			t.Fatalf("WorkDirFromContext = %q, want inferred %q", got, wantDir)
		}
	case <-time.After(time.Second):
		t.Fatal("spawn function never called")
	}
}

func TestSpawnAgentInfersWorkDirFromTildeDelegatedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wantDir := filepath.Join(home, "git", "deepseek")
	if err := os.MkdirAll(wantDir, 0o700); err != nil {
		t.Fatal(err)
	}
	gotCtx := make(chan context.Context, 1)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		gotCtx <- ctx
		return "ok", nil
	})
	tool := NewSpawnAgent(pool)

	_, err := tool.Execute(context.Background(), map[string]any{
		"task_description": "review forge UI and compare against ~/git/deepseek",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ctxv := <-gotCtx:
		if got := react.WorkDirFromContext(ctxv); got != wantDir {
			t.Fatalf("WorkDirFromContext = %q, want inferred %q", got, wantDir)
		}
	case <-time.After(time.Second):
		t.Fatal("spawn function never called")
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
	if waitPayload["resume_supported"] != false {
		t.Fatalf("resume_supported = %#v, want false", waitPayload["resume_supported"])
	}
	if hint, _ := waitPayload["resume_hint"].(string); !strings.Contains(hint, "cannot be resumed") {
		t.Fatalf("resume_hint = %#v, want explicit cannot-resume guidance", waitPayload["resume_hint"])
	}
}

func TestGetAgentOutputToolReturnsCompletionEnvelope(t *testing.T) {
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "result text", nil
	})
	spawn := NewSpawnAgent(pool)
	output := NewGetAgentOutput(pool)

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

	rawOutput, err := output.Execute(context.Background(), map[string]any{
		"id":              id,
		"timeout_seconds": 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var outputPayload map[string]any
	if err := json.Unmarshal([]byte(rawOutput), &outputPayload); err != nil {
		t.Fatal(err)
	}
	if outputPayload["status"] != string(react.AgentStatusCompleted) {
		t.Fatalf("status = %#v", outputPayload["status"])
	}
	if outputPayload["result"] != "result text" {
		t.Fatalf("result = %#v", outputPayload["result"])
	}
}

func TestWaitAgentToolRedactsResultErrorAndActivity(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "result " + secret, errors.New("error " + secret)
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{"task_description": "inspect repo"})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	pool.RecordProgress(id, "run_command", "printed "+secret)

	rawWait, err := wait.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawWait, secret) {
		t.Fatalf("wait_agent leaked secret: %s", rawWait)
	}
	if !strings.Contains(rawWait, "REDACTED:generic-token") {
		t.Fatalf("wait_agent missing redaction marker: %s", rawWait)
	}
}

func TestAgentStatusToolReturnsAgents(t *testing.T) {
	release := make(chan struct{})
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		<-release
		return "done", nil
	})
	spawn := NewSpawnAgent(pool)
	statusTool := NewAgentStatus(pool)

	if _, err := spawn.Execute(context.Background(), map[string]any{
		"task_description": "inspect repo",
		"role":             "repo-auditor",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := statusTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Agents []react.AgentResult `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Agents) != 1 || payload.Agents[0].Status != react.AgentStatusRunning || payload.Agents[0].Role != "repo-auditor" {
		t.Fatalf("status payload = %#v", payload)
	}
	close(release)
}

func TestAgentStatusToolReturnsEmptyAgentsArray(t *testing.T) {
	statusTool := NewAgentStatus(react.NewAgentPool(nil))

	raw, err := statusTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Agents []react.AgentResult `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Agents == nil || len(payload.Agents) != 0 {
		t.Fatalf("agents = %#v, want empty non-nil array from %s", payload.Agents, raw)
	}
}

func TestAgentStatusToolRedactsResultErrorAndActivity(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "result " + secret, errors.New("error " + secret)
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)
	statusTool := NewAgentStatus(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{"task_description": "inspect repo"})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	pool.RecordProgress(id, "run_command", "printed "+secret)
	if _, err := wait.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0}); err != nil {
		t.Fatal(err)
	}

	rawStatus, err := statusTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawStatus, secret) {
		t.Fatalf("agent_status leaked secret: %s", rawStatus)
	}
	if !strings.Contains(rawStatus, "REDACTED:generic-token") {
		t.Fatalf("agent_status missing redaction marker: %s", rawStatus)
	}
}

func TestWaitAgentRedactsHandoffPayload(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "report\n```forge_handoff\n{" +
			`"remaining_actions":[{"kind":"restore_file","target_path":"README.md ` + secret + `","description":"restore ` + secret + `","suggested_command":"git restore README.md # ` + secret + `","blocking":true}],` +
			`"incidents":[{"kind":"accidental_write","paths":["README.md ` + secret + `"],"description":"incident ` + secret + `","blocking":true}]` +
			"}\n```", nil
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{"task_description": "inspect repo"})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	rawWait, err := wait.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawWait, secret) {
		t.Fatalf("wait_agent leaked handoff secret: %s", rawWait)
	}
	if !strings.Contains(rawWait, "REDACTED:generic-token") {
		t.Fatalf("wait_agent missing redaction marker: %s", rawWait)
	}
}

func TestKillAgentToolCancelsRunningAgent(t *testing.T) {
	started := make(chan struct{})
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	spawn := NewSpawnAgent(pool)
	killTool := NewKillAgent(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{
		"task_description": "long task",
	})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	<-started
	rawKill, err := killTool.Execute(context.Background(), map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	var killPayload map[string]any
	if err := json.Unmarshal([]byte(rawKill), &killPayload); err != nil {
		t.Fatal(err)
	}
	if killPayload["status"] != string(react.AgentStatusKilled) {
		t.Fatalf("kill payload = %#v", killPayload)
	}
}

func TestKillAgentToolRedactsTerminalResultErrorAndActivity(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "result " + secret, errors.New("error " + secret)
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)
	killTool := NewKillAgent(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{"task_description": "inspect repo"})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	pool.RecordProgress(id, "run_command", "printed "+secret)
	if _, err := wait.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0}); err != nil {
		t.Fatal(err)
	}

	rawKill, err := killTool.Execute(context.Background(), map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawKill, secret) {
		t.Fatalf("kill_agent leaked secret: %s", rawKill)
	}
	if !strings.Contains(rawKill, "REDACTED:generic-token") {
		t.Fatalf("kill_agent missing redaction marker: %s", rawKill)
	}
}

func TestAgentStatusToolReflectsRunningAgentAfterWaitTimeout(t *testing.T) {
	release := make(chan struct{})
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		<-release
		return "done", nil
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)
	statusTool := NewAgentStatus(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{
		"task_description": "inspect repo",
		"role":             "repo-auditor",
	})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	pool.RecordProgress(id, "read_file", "README.md")
	if _, err := wait.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 0.01}); err != nil {
		t.Fatal(err)
	}

	rawStatus, err := statusTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Agents []react.AgentResult `json:"agents"`
	}
	if err := json.Unmarshal([]byte(rawStatus), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Agents) != 1 || payload.Agents[0].Status != react.AgentStatusRunning {
		t.Fatalf("status payload = %#v", payload)
	}
	if payload.Agents[0].LastToolName != "read_file" || len(payload.Agents[0].RecentActivity) != 1 {
		t.Fatalf("status progress = %#v", payload.Agents[0])
	}
	close(release)
}

func TestAgentStatusToolReportsTerminalAgentsCannotResume(t *testing.T) {
	pool := react.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "done", nil
	})
	spawn := NewSpawnAgent(pool)
	wait := NewWaitAgent(pool)
	statusTool := NewAgentStatus(pool)

	rawSpawn, err := spawn.Execute(context.Background(), map[string]any{"task_description": "inspect repo"})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	if _, err := wait.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0}); err != nil {
		t.Fatal(err)
	}

	rawStatus, err := statusTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal([]byte(rawStatus), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Agents) != 1 {
		t.Fatalf("status payload = %#v", payload)
	}
	if payload.Agents[0]["resume_supported"] != false {
		t.Fatalf("resume_supported = %#v, want false", payload.Agents[0]["resume_supported"])
	}
	if hint, _ := payload.Agents[0]["resume_hint"].(string); !strings.Contains(hint, "cannot be resumed") {
		t.Fatalf("resume_hint = %#v, want explicit cannot-resume guidance", payload.Agents[0]["resume_hint"])
	}
}
