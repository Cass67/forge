package react

import (
	"context"
	"errors"
	"strings"
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

func TestAgentPoolUpdatesSessionAgentTaskOnSpawnAndComplete(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("delegate audit")
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return role + ":" + task, nil
	})
	pool.AttachSession(session)

	id, err := pool.Spawn(context.Background(), "repo-auditor", "inspect repo")
	if err != nil {
		t.Fatal(err)
	}
	result, err := pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusCompleted {
		t.Fatalf("wait result = %#v", result)
	}

	tasks := session.Snapshot().AgentTasks
	if len(tasks) != 1 {
		t.Fatalf("agent tasks = %#v", tasks)
	}
	task := tasks[0]
	if task.ID != id || task.Role != "repo-auditor" || task.Prompt != "inspect repo" || task.ParentTurn != turn {
		t.Fatalf("agent task identity = %#v", task)
	}
	if task.Status != AgentStatusCompleted || task.Result != "repo-auditor:inspect repo" || task.Error != "" {
		t.Fatalf("agent task terminal state = %#v", task)
	}
	if task.CreatedAt.IsZero() || task.StartedAt.IsZero() || task.CompletedAt.IsZero() || task.LastActivityAt.IsZero() {
		t.Fatalf("agent task timestamps = %#v", task)
	}
}

func TestAgentPoolStoresParsedHandoffOnCompletion(t *testing.T) {
	session := NewSession()
	session.RecordInput("delegate audit")
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "audit report\n\n```forge_handoff\n{" +
			`"remaining_actions":[{"kind":"write_file","target_path":"docs/audit.md","description":"Save report","blocking":true}],` +
			`"incidents":[{"kind":"accidental_write","paths":["README.md"],"description":"Child wrote report into README","blocking":true}]` +
			"}\n```", nil
	})
	pool.AttachSession(session)

	id, err := pool.Spawn(context.Background(), "repo-auditor", "inspect repo")
	if err != nil {
		t.Fatal(err)
	}
	result, err := pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != "audit report" {
		t.Fatalf("result = %q, want sanitized report", result.Result)
	}
	if result.Handoff == nil || !result.Handoff.Blocking() {
		t.Fatalf("result handoff = %#v, want blocking handoff", result.Handoff)
	}
	if result.Handoff.RemainingActions[0].TargetPath != "docs/audit.md" {
		t.Fatalf("result handoff actions = %#v", result.Handoff.RemainingActions)
	}

	tasks := session.Snapshot().AgentTasks
	if len(tasks) != 1 {
		t.Fatalf("agent tasks = %#v", tasks)
	}
	if tasks[0].Result != "audit report" {
		t.Fatalf("task result = %q, want sanitized report", tasks[0].Result)
	}
	if tasks[0].Handoff == nil || !tasks[0].Handoff.Blocking() {
		t.Fatalf("task handoff = %#v, want blocking handoff", tasks[0].Handoff)
	}
}

func TestAgentPoolHandoffRemovesUserRepairCommandFromReport(t *testing.T) {
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		return "I accidentally overwrote README.md.\nTo fix this, run `git restore --source=HEAD -- README.md`.\n" +
			"Do not run `rm README.md`.\n" +
			"Do not execute apply_patch yourself.\n" +
			"Do not run `git reset --hard`.\n\n```forge_handoff\n{" +
			`"incidents":[{"kind":"accidental_write","paths":["README.md"],"description":"Child wrote report into README","blocking":true}],` +
			`"remaining_actions":[{"kind":"restore_file","target_path":"README.md","suggested_command":"git restore --source=HEAD -- README.md","description":"Inspect diff, then restore if safe","blocking":true}]` +
			"}\n```", nil
	})

	id, err := pool.Spawn(context.Background(), "repo-auditor", "audit")
	if err != nil {
		t.Fatal(err)
	}
	result, err := pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Result, "git restore") || strings.Contains(result.Result, "rm README") || strings.Contains(result.Result, "apply_patch") || strings.Contains(result.Result, "git reset") || strings.Contains(strings.ToLower(result.Result), "run `") {
		t.Fatalf("result leaked user repair command: %q", result.Result)
	}
	if result.Handoff == nil || result.Handoff.RemainingActions[0].SuggestedCommand == "" {
		t.Fatalf("handoff did not preserve parent repair action: %#v", result.Handoff)
	}
}

func TestAgentPoolUpdatesSessionAgentTaskOnTimeoutFailureAndNotFound(t *testing.T) {
	session := NewSession()
	session.RecordInput("delegate work")
	started := make(chan struct{})
	release := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(started)
		<-release
		return "", errors.New("boom")
	})
	pool.AttachSession(session)

	id, err := pool.Spawn(context.Background(), "worker", "change file")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := pool.Wait(context.Background(), id, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if task := session.Snapshot().AgentTasks[0]; task.Status != AgentStatusRunning {
		t.Fatalf("task after timeout = %#v", task)
	}
	close(release)
	if _, err := pool.Wait(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
	if task := session.Snapshot().AgentTasks[0]; task.Status != AgentStatusFailed || task.Error == "" {
		t.Fatalf("task after failure = %#v", task)
	}

	if _, err := pool.Wait(context.Background(), "missing-agent", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	tasks := session.Snapshot().AgentTasks
	if len(tasks) != 2 || tasks[1].ID != "missing-agent" || tasks[1].Status != AgentStatusNotFound {
		t.Fatalf("tasks after not_found = %#v", tasks)
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
	if result.Status != AgentStatusRunning {
		t.Fatalf("status = %q, want %q", result.Status, AgentStatusRunning)
	}
	close(release)
}

func TestAgentPoolWaitTimeoutKeepsAgentRunning(t *testing.T) {
	session := NewSession()
	session.RecordInput("delegate work")
	started := make(chan struct{})
	release := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(started)
		<-release
		return "done", nil
	})
	pool.AttachSession(session)

	id, err := pool.Spawn(context.Background(), "worker", "inspect large repo")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	result, err := pool.Wait(context.Background(), id, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusRunning {
		t.Fatalf("wait timeout status = %q, want %q", result.Status, AgentStatusRunning)
	}
	if task := session.Snapshot().AgentTasks[0]; task.Status != AgentStatusRunning {
		t.Fatalf("task after wait timeout = %#v", task)
	}

	close(release)
	result, err = pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusCompleted || result.Result != "done" {
		t.Fatalf("result after completion = %#v", result)
	}
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

func TestAgentPoolStatusListsRunningAndCompletedAgents(t *testing.T) {
	release := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		if role == "running" {
			<-release
		}
		return role, nil
	})

	runningID, err := pool.Spawn(context.Background(), "running", "wait")
	if err != nil {
		t.Fatal(err)
	}
	completedID, err := pool.Spawn(context.Background(), "done", "finish")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Wait(context.Background(), completedID, time.Second); err != nil {
		t.Fatal(err)
	}

	statuses := pool.Statuses()
	if len(statuses) != 2 {
		t.Fatalf("statuses = %#v", statuses)
	}
	if statuses[0].ID != runningID || statuses[0].Status != AgentStatusRunning {
		t.Fatalf("running status = %#v", statuses[0])
	}
	if statuses[1].ID != completedID || statuses[1].Status != AgentStatusCompleted {
		t.Fatalf("completed status = %#v", statuses[1])
	}
	close(release)
}

func TestAgentPoolKillCancelsRunningAgentAndUpdatesSession(t *testing.T) {
	session := NewSession()
	session.RecordInput("run child")
	started := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	pool.AttachSession(session)

	id, err := pool.Spawn(context.Background(), "worker", "long task")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	result, err := pool.Kill(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AgentStatusKilled {
		t.Fatalf("kill result = %#v", result)
	}
	if task := session.Snapshot().AgentTasks[0]; task.Status != AgentStatusKilled {
		t.Fatalf("session task after kill = %#v", task)
	}
	waitResult, err := pool.Wait(context.Background(), id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if waitResult.Status != AgentStatusKilled {
		t.Fatalf("wait after kill = %#v", waitResult)
	}
}

func TestAgentPoolRecordsProgressInSession(t *testing.T) {
	session := NewSession()
	session.RecordInput("delegate audit")
	release := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		<-release
		return "done", nil
	})
	pool.AttachSession(session)

	id, err := pool.Spawn(context.Background(), "repo-auditor", "inspect repo")
	if err != nil {
		t.Fatal(err)
	}
	pool.RecordProgress(id, "read_file", "README.md")
	pool.RecordProgress(id, "git_status", "")

	task := session.Snapshot().AgentTasks[0]
	if task.LastToolName != "git_status" || len(task.RecentActivity) != 2 || task.RecentActivity[0].ToolName != "read_file" {
		t.Fatalf("task progress = %#v", task)
	}
	close(release)
}

func TestAgentPoolLifecycleObserverReceivesProgressUpdates(t *testing.T) {
	release := make(chan struct{})
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		<-release
		return "done", nil
	})
	var observed []AgentTaskState
	pool.SetLifecycleObserver(func(state AgentTaskState) {
		observed = append(observed, state)
	})

	id, err := pool.Spawn(context.Background(), "repo-auditor", "inspect repo")
	if err != nil {
		t.Fatal(err)
	}
	pool.RecordProgress(id, "read_file", "README.md")
	close(release)
	if _, err := pool.Wait(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}

	for _, state := range observed {
		if state.ID == id && state.LastToolName == "read_file" && len(state.RecentActivity) == 1 {
			return
		}
	}
	t.Fatalf("observer states = %#v, want progress update for %s", observed, id)
}

func TestAgentPoolAddsAgentIDToSpawnContext(t *testing.T) {
	seenID := make(chan string, 1)
	pool := NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		seenID <- AgentIDFromContext(ctx)
		return "done", nil
	})

	id, err := pool.Spawn(context.Background(), "repo-auditor", "inspect repo")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seenID:
		if got != id {
			t.Fatalf("context agent id = %q, want %q", got, id)
		}
	case <-time.After(time.Second):
		t.Fatal("spawn did not run")
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
		if !def.ReadOnly {
			t.Fatalf("default agent %q should be read-only", def.Name)
		}
		if def.SystemPrompt == "" {
			t.Fatalf("default agent %q missing system prompt", def.Name)
		}
		for _, blocked := range []string{"write_file", "edit_file", "apply_patch", "artifact_write", "run_command", "exec_session_start", "command_write_stdin"} {
			if containsString(def.Tools, blocked) {
				t.Fatalf("read-only default agent %q has mutation tool %q in %#v", def.Name, blocked, def.Tools)
			}
		}
	}

	for _, want := range []string{"repo-auditor", "code-reviewer", "explorer", "oracle", "synthesizer"} {
		if !names[want] {
			t.Fatalf("default agents missing %q in %#v", want, names)
		}
	}
}

func TestDefaultSynthesizerDoesNotInspectRepos(t *testing.T) {
	defs := DefaultAgentDefinitions()
	var synthesizer *AgentDefinition
	for i := range defs {
		if defs[i].Name == "synthesizer" {
			synthesizer = &defs[i]
			break
		}
	}
	if synthesizer == nil {
		t.Fatal("default agents missing synthesizer")
	}

	for _, blocked := range []string{
		"read_file", "list_dir", "search", "code_search", "glob", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
		"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status",
	} {
		if containsString(synthesizer.Tools, blocked) {
			t.Fatalf("synthesizer tools = %#v, should not include repo inspection tool %q", synthesizer.Tools, blocked)
		}
	}
	for _, want := range []string{
		"Use only evidence included in the task prompt",
		"Do not ask the user to paste files",
		"Do not claim missing filesystem or search tools",
	} {
		if !strings.Contains(synthesizer.SystemPrompt, want) {
			t.Fatalf("synthesizer prompt = %q, want %q", synthesizer.SystemPrompt, want)
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
