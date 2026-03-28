package harness

import (
	"context"
	"strings"
	"sync"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/llm"
	"forge/internal/skills"
)

type sequenceDriver struct {
	mu        sync.Mutex
	responses []string
	prompts   []string
}

func (d *sequenceDriver) Name() string { return "sequence" }

func (d *sequenceDriver) Stream(_ context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			d.prompts = append(d.prompts, messages[i].Content)
			break
		}
	}
	response := ""
	if len(d.responses) > 0 {
		response = d.responses[0]
		d.responses = d.responses[1:]
	}
	out <- llm.Token{Text: response}
	return nil
}

func (d *sequenceDriver) promptsSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.prompts...)
}

type inspectingDriver struct {
	mu                sync.Mutex
	response          string
	callCount         int
	lastInjectedSkill string
}

func (d *inspectingDriver) Name() string { return "inspecting" }

func (d *inspectingDriver) Stream(_ context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callCount++
	d.lastInjectedSkill = ""
	for _, msg := range messages {
		if msg.Role != llm.RoleUser || !strings.HasPrefix(msg.Content, "[Skill: ") {
			continue
		}
		rest := strings.TrimPrefix(msg.Content, "[Skill: ")
		if end := strings.Index(rest, "]"); end >= 0 {
			d.lastInjectedSkill = rest[:end]
			break
		}
	}
	out <- llm.Token{Text: d.response}
	return nil
}

func TestWorkersUseInjectedSkillContext(t *testing.T) {
	driver := &inspectingDriver{
		response: `{"status":"complete","changes":[{"path":"tools/cleanup_workspace.sh","summary":"Added a cleanup helper."}],"verification_attempts":[],"remaining_issues":[],"suggested_next":"none"}`,
	}
	manager := NewManager(ManagerConfig{
		WorkDir: ".",
		DriverFor: func(WorkerKind) llm.Driver {
			return driver
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerEditor,
		Objective: "implement a cleanup helper",
		TopicKey:  "workspace:directory",
		SkillContext: WorkerSkillContext{
			AutoMode: skills.AutoSkillsAuto,
			Loaded: []skills.Skill{{
				Name:        "test-driven-development",
				Description: "write tests first",
				Body:        "Write a failing test before implementation.",
				Source:      "/tmp/tdd/SKILL.md",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != ObservationComplete {
		t.Fatalf("observation = %#v", obs)
	}
	if driver.lastInjectedSkill != "test-driven-development" {
		t.Fatalf("lastInjectedSkill = %q", driver.lastInjectedSkill)
	}
	if len(obs.SkillUses) != 1 || obs.SkillUses[0].Name != "test-driven-development" {
		t.Fatalf("skill uses = %#v", obs.SkillUses)
	}
}

func TestWorkersMissingRequiredSkillFailsClosed(t *testing.T) {
	driver := &inspectingDriver{
		response: `{"status":"complete","changes":[{"path":"tools/cleanup_workspace.sh","summary":"Added a cleanup helper."}],"verification_attempts":[],"remaining_issues":[],"suggested_next":"none"}`,
	}
	manager := NewManager(ManagerConfig{
		WorkDir: ".",
		DriverFor: func(WorkerKind) llm.Driver {
			return driver
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerEditor,
		Objective: "implement a cleanup helper",
		TopicKey:  "workspace:directory",
		SkillContext: WorkerSkillContext{
			AutoMode: skills.AutoSkillsAuto,
		},
	})
	if err == nil {
		t.Fatal("expected missing required skill error")
	}
	if obs.Status != ObservationBlocked {
		t.Fatalf("observation = %#v", obs)
	}
	if !strings.Contains(obs.Summary, "required skill") {
		t.Fatalf("observation summary = %q", obs.Summary)
	}
	if driver.callCount != 0 {
		t.Fatalf("driver call count = %d, want 0", driver.callCount)
	}
	if len(obs.SkillUses) != 1 || obs.SkillUses[0].Outcome != "required_missing" {
		t.Fatalf("skill uses = %#v", obs.SkillUses)
	}
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

func TestManagerExecuteRetryPromptIncludesStringLiteralEscapeGuidance(t *testing.T) {
	driver := &sequenceDriver{responses: []string{
		"{\"status\":\"complete\",\"changes\":[{\"path\":\"internal/runtime/chat.go\",\"summary\":\"bad\tvalue\"}],\"verification_attempts\":[],\"remaining_issues\":[],\"suggested_next\":\"none\"}",
		`{"status":"complete","changes":[{"path":"internal/runtime/chat.go","summary":"fixed escaping in output"}],"verification_attempts":[],"remaining_issues":[],"suggested_next":"none"}`,
	}}
	manager := NewManager(ManagerConfig{
		WorkDir: ".",
		DriverFor: func(WorkerKind) llm.Driver {
			return driver
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerEditor,
		Objective: "fix strict json escaping issue",
		TopicKey:  "workspace:repository",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != ObservationComplete {
		t.Fatalf("observation = %#v", obs)
	}
	prompts := driver.promptsSnapshot()
	if len(prompts) < 2 {
		t.Fatalf("expected retry prompt, prompts = %#v", prompts)
	}
	retryPrompt := prompts[1]
	if !strings.Contains(retryPrompt, "\\t") || !strings.Contains(retryPrompt, "\\n") {
		t.Fatalf("retry prompt missing string escape guidance: %q", retryPrompt)
	}
	if !strings.Contains(strings.ToLower(retryPrompt), "compact json object") {
		t.Fatalf("retry prompt missing compact-json guidance: %q", retryPrompt)
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

func TestManagerExecuteRecoversFromMalformedReaderToolMarkup(t *testing.T) {
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
				return "README explains the project layout.", nil
			}
			return "", nil
		},
	})

	manager := NewManager(ManagerConfig{
		WorkDir:   ".",
		BaseTools: reg,
		DriverFor: func(WorkerKind) llm.Driver {
			return &sequenceDriver{responses: []string{
				"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}{\"status\":\"complete\",\"evidence\":[{\"kind\":\"command\",\"summary\":\"Top-level listing shows the repo layout.\"}],\"coverage\":\"repo root\",\"gaps\":[],\"suggested_next\":\"none\"}",
				"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}\n</tool_call>",
				"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":40}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."},{"kind":"file","path":"README.md","summary":"README explains the project layout."}],"coverage":"repo root","gaps":[],"suggested_next":"none"}`,
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
	if len(result.Evidence) != 2 {
		t.Fatalf("artifact = %#v", result)
	}
	if result.Evidence[1].Kind != "file" || result.Evidence[1].Path != "README.md" {
		t.Fatalf("artifact = %#v", result)
	}
	if result.Coverage != "repo root" {
		t.Fatalf("artifact = %#v", result)
	}
}

func TestManagerExecuteRetriesRepositoryWalkthroughUntilRepresentativeFileIsRead(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "list_dir",
		Description: "list directory contents",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "README.md\ngo.mod\ninternal/\n", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "read file contents",
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			switch path {
			case "README.md":
				return "Forge is a terminal coding assistant.", nil
			case "go.mod":
				return "module forge", nil
			default:
				return "", nil
			}
		},
	})

	manager := NewManager(ManagerConfig{
		WorkDir:   ".",
		BaseTools: reg,
		DriverFor: func(WorkerKind) llm.Driver {
			return &sequenceDriver{responses: []string{
				"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."}],"coverage":"repo root","gaps":["No representative files inspected yet."],"suggested_next":"read README.md"}`,
				"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":40}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."},{"kind":"file","path":"README.md","summary":"README identifies Forge as a terminal coding assistant."}],"coverage":"repo root plus README","gaps":[],"suggested_next":"none"}`,
			}}
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:      WorkerReader,
		Objective: "have a look at this repo and tell me what you think",
		Context: `Gather concrete workspace evidence before you conclude.
For a directory or repository walkthrough:
- inspect the top-level structure with list_dir
- inspect one or two representative files such as README.md, go.mod, package.json, or a relevant entrypoint when present`,
		TopicKey: "workspace:repository",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := obs.Artifact.(ReaderResult)
	if !ok {
		t.Fatalf("artifact = %#v", obs.Artifact)
	}
	if len(result.Evidence) < 2 {
		t.Fatalf("expected representative file evidence after retry, got %#v", result)
	}
	if result.Evidence[1].Kind != "file" || result.Evidence[1].Path != "README.md" {
		t.Fatalf("expected README grounding after retry, got %#v", result)
	}
}

func TestManagerExecuteRetriesEvaluativeRepoReviewUntilNonReadmeFileIsRead(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "list_dir",
		Description: "list directory contents",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "README.md\n.pre-commit-config.yaml\nutil-ies-mgmt-03/\n", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "read file contents",
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			switch path {
			case "README.md":
				return "README explains the multi-host Cerner automation flows.", nil
			case ".pre-commit-config.yaml":
				return "repos:\n  - repo: https://github.com/astral-sh/ruff-pre-commit", nil
			default:
				return "", nil
			}
		},
	})

	manager := NewManager(ManagerConfig{
		WorkDir:   ".",
		BaseTools: reg,
		DriverFor: func(WorkerKind) llm.Driver {
			return &sequenceDriver{responses: []string{
				"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}\n</tool_call>",
				"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":80}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."},{"kind":"file","path":"README.md","summary":"README explains the multi-host automation layout."}],"coverage":"repo root plus README","gaps":["Need one non-README file for a grounded evaluative review."],"suggested_next":"read .pre-commit-config.yaml"}`,
				"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\".pre-commit-config.yaml\",\"start_line\":1,\"end_line\":80}}\n</tool_call>",
				`{"status":"complete","evidence":[{"kind":"command","summary":"Top-level listing shows the repo layout."},{"kind":"file","path":"README.md","summary":"README explains the multi-host automation layout."},{"kind":"file","path":".pre-commit-config.yaml","summary":"The pre-commit config layers secret scanning, Ruff, Bandit, and local guardrails, which suggests repo hygiene is enforced but duplicated Python checks may exist."}],"coverage":"repo root, README, and pre-commit config","gaps":[],"suggested_next":"inspect a producer entrypoint if deeper workflow risk analysis is needed"}`,
			}}
		},
	})

	obs, err := manager.Execute(context.Background(), WorkerTask{
		Kind:                         WorkerReader,
		Objective:                    "please take a look at this repo and tell me whats happeingin and what improvments could be made",
		TopicKey:                     "workspace:repository",
		RequireNonReadmeFileEvidence: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := obs.Artifact.(ReaderResult)
	if !ok {
		t.Fatalf("artifact = %#v", obs.Artifact)
	}
	if len(result.Evidence) < 3 {
		t.Fatalf("expected non-README evidence after retry, got %#v", result)
	}
	if result.Evidence[2].Kind != "file" || result.Evidence[2].Path != ".pre-commit-config.yaml" {
		t.Fatalf("expected non-README grounding after retry, got %#v", result)
	}
}
