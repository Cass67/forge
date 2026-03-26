package harness

import (
	"context"
	"sync"
	"testing"

	"forge/internal/agent/tools"
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

func TestManagerExecuteRetriesReaderWithoutConcreteEvidence(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "list_dir",
		Description: "list directory contents",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "go.mod\nREADME.md\ninternal/", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "read file contents",
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "go.mod" {
				return "module forge\nrequire github.com/charmbracelet/bubbletea v1.3.4", nil
			}
			return "", nil
		},
	})

	manager := NewManager(ManagerConfig{
		WorkDir:   ".",
		BaseTools: reg,
		DriverFor: func(WorkerKind) llm.Driver {
			return &sequenceDriver{responses: []string{
				"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"note","summary":"This looks like a Go project."}],"coverage":"ambient context only","gaps":["No files inspected."],"suggested_next":"inspect top-level files"}`,
				"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"go.mod\",\"start_line\":1,\"end_line\":40}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"file","path":"go.mod","summary":"go.mod declares the forge module and Bubble Tea dependencies."}],"coverage":"Checked the module file.","gaps":[],"suggested_next":"read README.md for a user-facing overview"}`,
			}}
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerReader,
		Objective: "talk about this directory",
		TopicKey:  "workspace:directory",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := obs.Artifact.(ReaderResult)
	if !ok {
		t.Fatalf("artifact = %#v", obs.Artifact)
	}
	if len(result.Evidence) != 1 {
		t.Fatalf("artifact = %#v", result)
	}
	if result.Evidence[0].Path != "go.mod" {
		t.Fatalf("artifact = %#v", result)
	}
}

func TestManagerExecuteRetriesUngroundedReaderEvidenceUntilItReadsTheClaimedFile(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "list_dir",
		Description: "list directory contents",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "README.md\ncmd/\ninternal/", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "read file contents",
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "README.md" {
				return "README explains the forge CLI.", nil
			}
			return "", nil
		},
	})

	manager := NewManager(ManagerConfig{
		WorkDir:   ".",
		BaseTools: reg,
		DriverFor: func(WorkerKind) llm.Driver {
			return &sequenceDriver{responses: []string{
				"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}\n</tool_call>{\"status\":\"complete\",\"evidence\":[{\"kind\":\"command\",\"summary\":\"Top-level listing shows the repo layout.\"}],\"coverage\":\"repo root\",\"gaps\":[\"Need to inspect a representative file.\"],\"suggested_next\":\"read README.md\"}",
				`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."},{"kind":"file","path":"README.md","summary":"README outlines the CLI."}],"coverage":"repo root","gaps":[],"suggested_next":"none"}`,
				"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":40}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"file","path":"README.md","summary":"README outlines the CLI."}],"coverage":"repo root","gaps":[],"suggested_next":"none"}`,
			}}
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerReader,
		Objective: "describe this directory",
		TopicKey:  "workspace:directory",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := obs.Artifact.(ReaderResult)
	if !ok {
		t.Fatalf("artifact = %#v", obs.Artifact)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Path != "README.md" {
		t.Fatalf("artifact = %#v", result)
	}
}
