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
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow, Reason: "safe test " + secret}}
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
	if event.Decision != permissions.ClassifierAllow || event.Reason != "safe test <REDACTED:github-pat>" || event.Fallback != "" || event.Error != "" {
		t.Fatalf("unexpected classifier event = %#v", event)
	}
	if strings.Contains(event.Action.Summary, secret) || strings.Contains(event.Action.Detail, secret) {
		t.Fatalf("classifier event leaked secret: %#v", event.Action)
	}
	if !strings.Contains(event.Action.Summary, "<REDACTED:github-pat>") || !strings.Contains(event.Action.Detail, "<REDACTED:github-pat>") {
		t.Fatalf("classifier event action was not redacted: %#v", event.Action)
	}
}

func TestAutoPermissionClassifierObserverRedactsActionPath(t *testing.T) {
	secret := dummyApprovalSecret()
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, nil, nil)

	gate.emitClassifierEvent(ClassifierEvent{
		Action: permissions.Action{Tool: "read_file", Path: "config/" + secret},
	})

	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if strings.Contains(events[0].Action.Path, secret) {
		t.Fatalf("classifier event path leaked secret: %#v", events[0].Action)
	}
	if !strings.Contains(events[0].Action.Path, "<REDACTED:github-pat>") {
		t.Fatalf("classifier event path was not redacted: %#v", events[0].Action)
	}
}

func TestAutoPermissionClassifierApprovalUpdateRedactsReason(t *testing.T) {
	secret := dummyApprovalSecret()
	cases := []struct {
		name     string
		decision permissions.ClassifierDecision
	}{
		{name: "allow", decision: permissions.ClassifierAllow},
		{name: "deny", decision: permissions.ClassifierDeny},
		{name: "ask", decision: permissions.ClassifierAsk},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: tc.decision, Reason: "contains " + secret}}
			gate := NewApprovalGate("", ApprovalConfig{
				DefaultPolicy: ApprovalUnlessTrusted,
				SandboxPolicy: SandboxDangerFull,
				Classifier:    classifier,
			}, func(action tools.Action) (bool, error) {
				return true, nil
			}, nil)

			if _, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "git push origin main", Detail: "git push origin main"}); err != nil {
				t.Fatal(err)
			}

			updates := gate.ApprovalUpdates()
			if len(updates) == 0 {
				t.Fatal("expected approval updates")
			}
			foundRedaction := false
			for _, update := range updates {
				if strings.Contains(update.Reason, secret) {
					t.Fatalf("approval update leaked classifier reason: %#v", update)
				}
				if strings.Contains(update.Reason, "<REDACTED:github-pat>") {
					foundRedaction = true
				}
			}
			if !foundRedaction {
				t.Fatalf("approval updates did not include redacted reason: %#v", updates)
			}
		})
	}
}

func TestAutoPermissionClassifierFallbackApprovalUpdateRedactsSummary(t *testing.T) {
	secret := dummyApprovalSecret()
	cases := []struct {
		name       string
		classifier *fakePermissionClassifier
		denials    *permissions.DenialTracker
		failure    ClassifierFailureBehavior
	}{
		{name: "denial tracker", classifier: &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow}}, denials: saturatedDenialTracker()},
		{name: "error ask", classifier: &fakePermissionClassifier{err: errors.New("parse failed")}},
		{name: "error deny", classifier: &fakePermissionClassifier{err: errors.New("parse failed")}, failure: ClassifierFailureDeny},
		{name: "unknown decision", classifier: &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierDecision("maybe")}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := NewApprovalGate("", ApprovalConfig{
				DefaultPolicy:             ApprovalUnlessTrusted,
				SandboxPolicy:             SandboxDangerFull,
				Classifier:                tc.classifier,
				Denials:                   tc.denials,
				ClassifierFailureBehavior: tc.failure,
			}, func(action tools.Action) (bool, error) {
				return true, nil
			}, nil)

			if _, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./... " + secret, Detail: "go test ./..."}); err != nil {
				t.Fatal(err)
			}

			assertApprovalUpdatesRedacted(t, gate.ApprovalUpdates(), secret)
		})
	}
}

func saturatedDenialTracker() *permissions.DenialTracker {
	denials := permissions.NewDenialTracker(3, 20)
	denials.RecordDenied()
	denials.RecordDenied()
	denials.RecordDenied()
	return denials
}

func assertApprovalUpdatesRedacted(t *testing.T, updates []ApprovalUpdate, secret string) {
	t.Helper()
	if len(updates) == 0 {
		t.Fatal("expected approval updates")
	}
	foundRedaction := false
	for _, update := range updates {
		if strings.Contains(update.Reason, secret) {
			t.Fatalf("approval update leaked secret: %#v", update)
		}
		if strings.Contains(update.Reason, "<REDACTED:github-pat>") {
			foundRedaction = true
		}
	}
	if !foundRedaction {
		t.Fatalf("approval updates did not include redacted detail: %#v", updates)
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
	classifier := &fakePermissionClassifier{err: errors.New("parse failed " + secret)}
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
	assertClassifierObserverEvent(t, events, permissions.ClassifierAsk, "", string(ClassifierFailureAsk), "parse failed <REDACTED:github-pat>", secret)
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

func TestAutoPermissionClassifierObserverReceivesDenialTrackerFallback(t *testing.T) {
	secret := dummyApprovalSecret()
	classifier := &fakePermissionClassifier{response: permissions.ClassifierResponse{Decision: permissions.ClassifierAllow}}
	denials := permissions.NewDenialTracker(3, 20)
	denials.RecordDenied()
	denials.RecordDenied()
	denials.RecordDenied()
	promptCalls := 0
	var events []ClassifierEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalUnlessTrusted,
		SandboxPolicy: SandboxDangerFull,
		Classifier:    classifier,
		Denials:       denials,
		ClassifierObserver: func(event ClassifierEvent) {
			events = append(events, event)
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./... " + secret, Detail: "TOKEN=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if !approved || promptCalls != 1 || classifier.calls != 0 {
		t.Fatalf("approved=%v promptCalls=%d classifierCalls=%d, want denial tracker prompt without classifier", approved, promptCalls, classifier.calls)
	}
	assertClassifierObserverEvent(t, events, permissions.ClassifierAsk, "classifier disabled after repeated denials", string(ClassifierFailureAsk), "", secret)
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
	if strings.Contains(event.Reason, secret) || strings.Contains(event.Error, secret) {
		t.Fatalf("classifier event leaked secret in reason or error: %#v", event)
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
