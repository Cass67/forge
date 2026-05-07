package react

import (
	"context"
	"errors"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/permissions"
)

type fakePermissionClassifier struct {
	calls    int
	requests []permissions.ClassifierRequest
	response permissions.ClassifierResponse
	err      error
}

func (f *fakePermissionClassifier) Classify(ctx context.Context, req permissions.ClassifierRequest) (permissions.ClassifierResponse, error) {
	f.calls++
	f.requests = append(f.requests, req)
	return f.response, f.err
}

func TestAutoPermissionClassifierRuleBlockBypassesClassifier(t *testing.T) {
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow}}
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		Rules: []ApprovalRule{{
			Tool:          "run_command",
			CommandPrefix: []string{"git", "push"},
			Decision:      DecisionForbidden,
		}},
	}, func(action tools.Action) (bool, error) { return true, nil }, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main", Detail: "git push origin main"})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected rule block")
	}
	if classifier.calls != 0 {
		t.Fatalf("classifier calls = %d, want 0", classifier.calls)
	}
}

func TestAutoPermissionClassifierAllowsLowRiskCommand(t *testing.T) {
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow, Reason: "safe test"}}
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./...", Detail: "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected classifier allow")
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls = %d, want 1", classifier.calls)
	}
	if classifier.requests[0].Risk.Level != permissions.RiskLow {
		t.Fatalf("risk = %#v", classifier.requests[0].Risk)
	}
}

func TestAutoPermissionClassifierObserverReceivesRedactedDecision(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow, Reason: "safe test"}}
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, nil, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./... " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected classifier allow")
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Decision != permissions.ClassifierAllow || event.Reason != "safe test" || event.Fallback != "" || event.Error != "" {
		t.Fatalf("unexpected classifier event = %#v", event)
	}
	if strings.Contains(event.Action.Summary, secret) || strings.Contains(event.Action.Detail, secret) {
		t.Fatalf("classifier event leaked secret: %#v", event.Action)
	}
	if !strings.Contains(event.Action.Summary, "<REDACTED:github-pat>") || !strings.Contains(event.Action.Detail, "<REDACTED:github-pat>") {
		t.Fatalf("classifier event action was not redacted: %#v", event.Action)
	}
}

func TestAutoPermissionClassifierObserverReceivesRedactedDeny(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierDeny, Reason: "unsafe command"}}
	promptCalls := 0
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if approved || promptCalls != 0 {
		t.Fatalf("approved=%v promptCalls=%d, want deny without prompt", approved, promptCalls)
	}
	assertClassifierObserverEvent(t, events, permissions.ClassifierDeny, "unsafe command", "", "", secret)
}

func TestAutoPermissionClassifierObserverReceivesRedactedAskFallback(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAsk, Reason: "needs review"}}
	promptCalls := 0
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 {
		t.Fatalf("approved=%v promptCalls=%d, want approved prompt", approved, promptCalls)
	}
	assertClassifierObserverEvent(t, events, permissions.ClassifierAsk, "needs review", string(ClassifierFailureAsk), "", secret)
}

func TestAutoPermissionClassifierObserverReceivesRedactedErrorFallbackAsk(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{err: errors.New("parse failed")}
	promptCalls := 0
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 {
		t.Fatalf("approved=%v promptCalls=%d, want approved prompt", approved, promptCalls)
	}
	assertClassifierObserverEvent(t, events, permissions.ClassifierAsk, "", string(ClassifierFailureAsk), "parse failed", secret)
}

func TestAutoPermissionClassifierObserverReceivesRedactedErrorFallbackDeny(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{err: errors.New("parse failed")}
	promptCalls := 0
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:             ApprovalUnlessTrusted,
		SandboxPolicy:             SandboxDangerFull,
		Classifier:                classifier,
		ClassifierFailureBehavior: ClassifierFailureDeny,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if approved || promptCalls != 0 {
		t.Fatalf("approved=%v promptCalls=%d, want deny without prompt", approved, promptCalls)
	}
	assertClassifierObserverEvent(t, events, permissions.ClassifierDeny, "", string(ClassifierFailureDeny), "parse failed", secret)
}

func TestAutoPermissionClassifierObserverReceivesRedactedImmunePromptFallback(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow}}
	promptCalls := 0
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "rm -rf / " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 || classifier.calls != 0 {
		t.Fatalf("approved=%v promptCalls=%d classifierCalls=%d, want immune prompt without classifier", approved, promptCalls, classifier.calls)
	}
	assertClassifierObserverEvent(t, events, permissions.ClassifierAsk, "classifier-immune action", string(ClassifierFailureAsk), "", secret)
}

func assertClassifierObserverEvent(t *testing.T, events []ClassifierEvent, decision permissions.ClassifierDecision, reason, fallback, errorText, secret string) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Decision != decision || event.Reason != reason || event.Fallback != fallback || event.Error != errorText {
		t.Fatalf("unexpected classifier event = %#v", event)
	}
	if strings.Contains(event.Action.Summary, secret) || strings.Contains(event.Action.Detail, secret) {
		t.Fatalf("classifier event leaked secret: %#v", event.Action)
	}
	if !strings.Contains(event.Action.Summary, "<REDACTED:github-pat>") || !strings.Contains(event.Action.Detail, "<REDACTED:github-pat>") {
		t.Fatalf("classifier event action was not redacted: %#v", event.Action)
	}
}

func TestAutoPermissionClassifierAllowsMediumRiskEdit(t *testing.T) {
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow, Reason: "normal edit"}}
	gate := NewApprovalGate("", ApprovalConfig{DefaultPolicy: ApprovalUnlessTrusted, SandboxPolicy: SandboxDangerFull, Classifier: classifier}, nil, nil)

	approved, err := gate.Approve(tools.Action{Tool: "edit_file", Summary: "edit main.go", Detail: "diff"})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected medium-risk edit to be classifier-allowed")
	}
	if classifier.requests[0].Risk.Level != permissions.RiskMedium || classifier.requests[0].Risk.Destructive {
		t.Fatalf("unexpected risk = %#v", classifier.requests[0].Risk)
	}
}

func TestAutoPermissionClassifierHighRiskAskPrompts(t *testing.T) {
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAsk, Reason: "high risk"}}
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{DefaultPolicy: ApprovalUnlessTrusted, SandboxPolicy: SandboxDangerFull, Classifier: classifier}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main", Detail: "git push origin main"})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 {
		t.Fatalf("approved=%v promptCalls=%d, want approved prompt", approved, promptCalls)
	}
}

func TestAutoPermissionClassifierImmuneActionPromptsWithoutClassifier(t *testing.T) {
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow}}
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{DefaultPolicy: ApprovalUnlessTrusted, SandboxPolicy: SandboxDangerFull, Classifier: classifier}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "rm -rf /", Detail: "rm -rf /"})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 {
		t.Fatalf("approved=%v promptCalls=%d, want prompt", approved, promptCalls)
	}
	if classifier.calls != 0 {
		t.Fatalf("classifier calls = %d, want 0", classifier.calls)
	}
}

func TestAutoPermissionClassifierFailureAsks(t *testing.T) {
	classifier := &fakePermissionClassifier{err: errors.New("parse failed")}
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{DefaultPolicy: ApprovalUnlessTrusted, SandboxPolicy: SandboxDangerFull, Classifier: classifier}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./...", Detail: "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 {
		t.Fatalf("approved=%v promptCalls=%d, want prompt", approved, promptCalls)
	}
}

func TestAutoPermissionClassifierFailureCanDeny(t *testing.T) {
	classifier := &fakePermissionClassifier{err: errors.New("parse failed")}
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:             ApprovalUnlessTrusted,
		SandboxPolicy:             SandboxDangerFull,
		Classifier:                classifier,
		ClassifierFailureBehavior: ClassifierFailureDeny,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./...", Detail: "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if approved || promptCalls != 0 {
		t.Fatalf("approved=%v promptCalls=%d, want deny without prompt", approved, promptCalls)
	}
}

func TestAutoPermissionClassifierDenialTrackerFallbackPrompts(t *testing.T) {
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow}}
	denials := permissions.NewDenialTracker(3, 20)
	denials.RecordDenied()
	denials.RecordDenied()
	denials.RecordDenied()
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		Denials:       denials,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./...", Detail: "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 || classifier.calls != 0 {
		t.Fatalf("approved=%v promptCalls=%d classifierCalls=%d", approved, promptCalls, classifier.calls)
	}
}
