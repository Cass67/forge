package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubLocalExecutor struct {
	obs   Observation
	err   error
	calls int
}

func (s *stubLocalExecutor) Execute(_ context.Context, _ UserTurn, _ Classification) (Observation, error) {
	s.calls++
	return s.obs, s.err
}

type stubWorkerExecutor struct {
	obs   Observation
	err   error
	calls int
	task  WorkerTask
}

func (s *stubWorkerExecutor) Execute(_ context.Context, task WorkerTask) (Observation, error) {
	s.calls++
	s.task = task
	return s.obs, s.err
}

func TestRunnerLocalInspectUsesLocalStep(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "Directory contains cmd and internal.",
			Summary:  "directory overview",
			TopicKey: "workspace:directory",
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
	})

	result, err := runner.Run(context.Background(), "describe this directory")
	if err != nil {
		t.Fatal(err)
	}
	if local.calls != 1 {
		t.Fatalf("local calls = %d", local.calls)
	}
	if result.Classification.Family != FamilyInspect {
		t.Fatalf("family = %q", result.Classification.Family)
	}
	if result.Step.Kind != StepLocal {
		t.Fatalf("step = %#v", result.Step)
	}
	if result.Response != "Directory contains cmd and internal." {
		t.Fatalf("response = %q", result.Response)
	}
	if len(result.Trace) == 0 {
		t.Fatal("expected trace records")
	}
}

func TestRunnerLocalImplementationUsesLocalStep(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "Updated the auth handler and added tests.",
			Summary:  "implementation complete",
			TopicKey: "path:internal/auth/handler.go",
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
	})

	result, err := runner.Run(context.Background(), "implement the auth handler fix")
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification.Family != FamilyImplement {
		t.Fatalf("family = %q", result.Classification.Family)
	}
	if result.Step.Kind != StepLocal || result.Step.Worker != WorkerNone {
		t.Fatalf("step = %#v", result.Step)
	}
}

func TestRunnerEvaluativeInspectUsesReaderWorkerWhenConfigured(t *testing.T) {
	local := &stubLocalExecutor{}
	worker := &stubWorkerExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Summary:  "repo review complete",
			TopicKey: "workspace:directory",
			Artifact: ReaderResult{
				Status: "complete",
				Evidence: []ReaderEvidence{
					{Kind: "command", Summary: "git_status shows many untracked generated files, indicating poor repo hygiene."},
					{Kind: "file", Path: "README.md", Summary: "README explains the active producer and consumer pipeline layout."},
				},
				Coverage:      "repo root",
				Gaps:          []string{},
				SuggestedNext: "none",
			},
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
		Workers: worker,
	})

	result, err := runner.Run(context.Background(), "have a look at the dir and repo within and recommend whats up and whats needed to be fixed")
	if err != nil {
		t.Fatal(err)
	}
	if result.Step.Kind != StepWorker || result.Step.Worker != WorkerReader {
		t.Fatalf("step = %#v", result.Step)
	}
	if worker.calls != 1 {
		t.Fatalf("worker calls = %d", worker.calls)
	}
	if local.calls != 0 {
		t.Fatalf("local should not have been used, got %d calls", local.calls)
	}
	if !strings.Contains(worker.task.Context, "evidence-backed findings") {
		t.Fatalf("worker context = %q", worker.task.Context)
	}
	if !strings.Contains(result.Response, "poor repo hygiene") {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestRunnerImplementationUsesEditorWorkerWhenConfigured(t *testing.T) {
	local := &stubLocalExecutor{}
	worker := &stubWorkerExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Summary:  "editor complete",
			TopicKey: "workspace:directory",
			Artifact: EditorResult{
				Status: "complete",
				Changes: []ChangeRecord{
					{Path: "tools/cleanup_workspace.sh", Summary: "Added a cleanup script for generated repo artifacts."},
				},
				VerificationAttempts: []VerificationAttempt{
					{Command: "bash -n tools/cleanup_workspace.sh", Outcome: "pass"},
				},
				RemainingIssues: []string{},
				SuggestedNext:   "run the script in dry-run mode",
			},
		},
	}
	session := NewSession()
	session.BeginTurn("have a look at this directory")
	session.Apply(Classification{Family: FamilyInspect, TopicKey: "workspace:directory"}, Observation{
		Status:   ObservationComplete,
		Response: "directory overview",
		Summary:  "repo hygiene issues and generated files at the root",
		TopicKey: "workspace:directory",
	})

	result, err := NewRunner(RunnerConfig{
		Session: session,
		Trace:   NewRecorder(),
		Local:   local,
		Workers: worker,
	}).Run(context.Background(), "can you write me a script to clean this up?")
	if err != nil {
		t.Fatal(err)
	}
	if result.Step.Kind != StepWorker || result.Step.Worker != WorkerEditor {
		t.Fatalf("step = %#v", result.Step)
	}
	if worker.calls != 1 {
		t.Fatalf("worker calls = %d", worker.calls)
	}
	if local.calls != 0 {
		t.Fatalf("local should not have been used, got %d calls", local.calls)
	}
	if worker.task.TopicKey != "workspace:directory" {
		t.Fatalf("worker topic = %q", worker.task.TopicKey)
	}
	if !strings.Contains(worker.task.Context, "repo hygiene issues and generated files at the root") {
		t.Fatalf("worker context = %q", worker.task.Context)
	}
	if !strings.Contains(result.Response, "cleanup script") {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestRunnerBlocksOnLocalFailure(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:  ObservationBlocked,
			Summary: "execution failed",
		},
		err: errors.New("execution failed"),
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
	})

	result, err := runner.Run(context.Background(), "describe this directory")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Decision.FinalState != StateBlocked {
		t.Fatalf("decision = %#v", result.Decision)
	}
}

func TestRunnerResearchUsesWorkerWhenConfigured(t *testing.T) {
	local := &stubLocalExecutor{}
	worker := &stubWorkerExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "Official docs describe the API.",
			Summary:  "Official docs describe the API.",
			TopicKey: "workspace:repository",
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
		Workers: worker,
	})

	result, err := runner.Run(context.Background(), "look up the latest API docs")
	if err != nil {
		t.Fatal(err)
	}
	if result.Step.Kind != StepWorker || result.Step.Worker != WorkerResearcher {
		t.Fatalf("step = %#v", result.Step)
	}
	if worker.calls != 1 {
		t.Fatalf("worker calls = %d", worker.calls)
	}
	if local.calls != 0 {
		t.Fatalf("local should not have been used, got %d calls", local.calls)
	}
	if worker.task.Objective != "look up the latest API docs" {
		t.Fatalf("worker task = %#v", worker.task)
	}
}

func TestRunnerFallsBackToLocalWhenWorkerFailsClosed(t *testing.T) {
	local := &stubLocalExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "Forge checked locally after the worker path failed.",
			Summary:  "local recovery complete",
			TopicKey: "workspace:repository",
		},
	}
	worker := &stubWorkerExecutor{
		obs: Observation{
			Status:   ObservationBlocked,
			Summary:  "worker unavailable",
			TopicKey: "workspace:repository",
		},
		err: errors.New("worker unavailable"),
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
		Workers: worker,
	})

	result, err := runner.Run(context.Background(), "look up the latest API docs")
	if err != nil {
		t.Fatal(err)
	}
	if worker.calls != 1 {
		t.Fatalf("worker calls = %d", worker.calls)
	}
	if local.calls != 1 {
		t.Fatalf("local calls = %d", local.calls)
	}
	if result.Step.Kind != StepLocal || result.Step.Worker != WorkerNone {
		t.Fatalf("step = %#v", result.Step)
	}
	if result.Response != "Forge checked locally after the worker path failed." {
		t.Fatalf("response = %q", result.Response)
	}
}

func TestRunnerSynthesizesWorkerResultIntoForgeResponse(t *testing.T) {
	local := &stubLocalExecutor{}
	worker := &stubWorkerExecutor{
		obs: Observation{
			Status:   ObservationComplete,
			Response: "raw worker prose should not be shown",
			Summary:  "research complete",
			TopicKey: "workspace:repository",
			Artifact: ResearcherResult{
				Status: "complete",
				Findings: []ResearchFinding{
					{Summary: "Official docs describe the API shape."},
					{Summary: "The guide recommends server-side auth."},
				},
				Sources: []ResearchSource{
					{Label: "official docs", Locator: "docs"},
				},
				Confidence: "high",
			},
		},
	}
	runner := NewRunner(RunnerConfig{
		Session: NewSession(),
		Trace:   NewRecorder(),
		Local:   local,
		Workers: worker,
	})

	result, err := runner.Run(context.Background(), "look up the latest API docs")
	if err != nil {
		t.Fatal(err)
	}
	if result.Response == "raw worker prose should not be shown" {
		t.Fatalf("worker response leaked directly: %q", result.Response)
	}
	if !strings.Contains(result.Response, "Official docs describe the API shape.") {
		t.Fatalf("response = %q", result.Response)
	}
	if !strings.Contains(result.Response, "Sources checked: official docs.") {
		t.Fatalf("response = %q", result.Response)
	}
}
