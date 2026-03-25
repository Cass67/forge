package harness

import (
	"context"
	"sync"
	"testing"

	"forge/internal/llm"
)

type sequenceDriver struct {
	mu        sync.Mutex
	responses []string
}

func (d *sequenceDriver) Name() string { return "sequence" }

func (d *sequenceDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.mu.Lock()
	defer d.mu.Unlock()
	response := ""
	if len(d.responses) > 0 {
		response = d.responses[0]
		d.responses = d.responses[1:]
	}
	out <- llm.Token{Text: response}
	return nil
}

func TestPlanResearchUsesResearcherWorker(t *testing.T) {
	step := Plan(Classification{
		Family:               FamilyResearch,
		NeedsExternalSources: true,
	}, SessionState{})
	if step.Kind != StepWorker || step.Worker != WorkerResearcher {
		t.Fatalf("step = %#v", step)
	}
}

func TestManagerExecuteRetriesInvalidStructuredOutput(t *testing.T) {
	manager := NewManager(ManagerConfig{
		WorkDir: ".",
		DriverFor: func(WorkerKind) llm.Driver {
			return &sequenceDriver{responses: []string{
				"not valid json",
				`{"status":"complete","findings":[{"summary":"Official docs describe the feature.","detail":"Looked up the reference."}],"sources":[{"label":"official docs","locator":"docs"}],"confidence":"high"}`,
			}}
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerResearcher,
		Objective: "look up the latest docs",
		TopicKey:  "workspace:repository",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != ObservationComplete {
		t.Fatalf("observation = %#v", obs)
	}
	if obs.Response != "" {
		t.Fatalf("raw worker response leaked into observation: %q", obs.Response)
	}
	result, ok := obs.Artifact.(ResearcherResult)
	if !ok {
		t.Fatalf("artifact = %#v", obs.Artifact)
	}
	if result.Findings[0].Summary != "Official docs describe the feature." {
		t.Fatalf("artifact = %#v", result)
	}
}
