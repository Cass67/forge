package harness

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type regressionFixture struct {
	Name               string        `json:"name"`
	Source             string        `json:"source"`
	Input              string        `json:"input"`
	Session            SessionState  `json:"session"`
	WantFamily         RequestFamily `json:"want_family"`
	WantStep           StepKind      `json:"want_step"`
	WantWorker         WorkerKind    `json:"want_worker"`
	WantTopicKey       string        `json:"want_topic_key"`
	WantEvaluation     bool          `json:"want_evaluation"`
	WantInterpretation bool          `json:"want_interpretation"`
	WantFollowUp       bool          `json:"want_follow_up"`
	WantPolicyGuard    bool          `json:"want_policy_guard"`
	WantTerseAnswer    bool          `json:"want_terse_answer"`
}

type previewBranchClaimRegressionFixture struct {
	Name                string            `json:"name"`
	Source              string            `json:"source"`
	Kind                string            `json:"kind"`
	Input               string            `json:"input"`
	Session             SessionState      `json:"session"`
	WantFamily          RequestFamily     `json:"want_family"`
	WantAction          bool              `json:"want_action"`
	WantFollowUp        bool              `json:"want_follow_up"`
	WantTopicKey        string            `json:"want_topic_key"`
	WantStep            StepKind          `json:"want_step"`
	WantWorker          WorkerKind        `json:"want_worker"`
	Step                Step              `json:"step"`
	Classification      Classification    `json:"classification"`
	Observation         Observation       `json:"observation"`
	WantStatus          ObservationStatus `json:"want_status"`
	WantOutcome         OutcomeKind       `json:"want_outcome"`
	WantReasonContains  string            `json:"want_reason_contains"`
	WantSummaryContains string            `json:"want_summary_contains"`
}

func TestRegressionFixturesRouteWithoutEscalation(t *testing.T) {
	paths := []string{
		filepath.Join("testdata", "debuglogs", "collaborative-routing.jsonl"),
		filepath.Join("testdata", "debuglogs", "repo-inspect-stall.jsonl"),
		filepath.Join("testdata", "debuglogs", "follow-up-misroute.jsonl"),
		filepath.Join("testdata", "debuglogs", "meta-answer-guard.jsonl"),
		filepath.Join("testdata", "debuglogs", "thread-ledger-routing.jsonl"),
	}
	for _, path := range paths {
		fixtures := loadRegressionFixtures(t, path)
		for _, fixture := range fixtures {
			t.Run(fixture.Name, func(t *testing.T) {
				class := Classify(UserTurn{Text: fixture.Input}, fixture.Session)
				if class.Family != fixture.WantFamily {
					t.Fatalf("%s family = %q, want %q", fixture.Source, class.Family, fixture.WantFamily)
				}
				if class.TopicKey != fixture.WantTopicKey {
					t.Fatalf("%s topic = %q, want %q", fixture.Source, class.TopicKey, fixture.WantTopicKey)
				}
				if class.WantsEvaluation != fixture.WantEvaluation {
					t.Fatalf("%s evaluation = %v, want %v", fixture.Source, class.WantsEvaluation, fixture.WantEvaluation)
				}
				if class.WantsInterpretation != fixture.WantInterpretation {
					t.Fatalf("%s interpretation = %v, want %v", fixture.Source, class.WantsInterpretation, fixture.WantInterpretation)
				}
				if class.IsFollowUp != fixture.WantFollowUp {
					t.Fatalf("%s follow_up = %v, want %v", fixture.Source, class.IsFollowUp, fixture.WantFollowUp)
				}
				if class.NeedsPolicyGuard != fixture.WantPolicyGuard {
					t.Fatalf("%s policy_guard = %v, want %v", fixture.Source, class.NeedsPolicyGuard, fixture.WantPolicyGuard)
				}
				if class.NeedsTerseAnswer != fixture.WantTerseAnswer {
					t.Fatalf("%s terse_answer = %v, want %v", fixture.Source, class.NeedsTerseAnswer, fixture.WantTerseAnswer)
				}
				step := Plan(class, fixture.Session)
				if step.Kind != fixture.WantStep {
					t.Fatalf("%s step = %q, want %q", fixture.Source, step.Kind, fixture.WantStep)
				}
				if step.Worker != fixture.WantWorker {
					t.Fatalf("%s worker = %q, want %q", fixture.Source, step.Worker, fixture.WantWorker)
				}
			})
		}
	}
}

func TestRegressionPreviewBranchClaimFixtures(t *testing.T) {
	path := filepath.Join("testdata", "preview_branch_claim_regression.json")
	fixtures := loadPreviewBranchClaimRegressionFixtures(t, path)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			switch fixture.Kind {
			case "classification":
				class := Classify(UserTurn{Text: fixture.Input}, fixture.Session)
				if class.Family != fixture.WantFamily {
					t.Fatalf("%s family = %q, want %q", fixture.Source, class.Family, fixture.WantFamily)
				}
				if class.WantsAction != fixture.WantAction {
					t.Fatalf("%s wants_action = %v, want %v", fixture.Source, class.WantsAction, fixture.WantAction)
				}
				if class.IsFollowUp != fixture.WantFollowUp {
					t.Fatalf("%s follow_up = %v, want %v", fixture.Source, class.IsFollowUp, fixture.WantFollowUp)
				}
				if class.TopicKey != fixture.WantTopicKey {
					t.Fatalf("%s topic = %q, want %q", fixture.Source, class.TopicKey, fixture.WantTopicKey)
				}
				step := Plan(class, fixture.Session)
				if step.Kind != fixture.WantStep {
					t.Fatalf("%s step = %q, want %q", fixture.Source, step.Kind, fixture.WantStep)
				}
				if step.Worker != fixture.WantWorker {
					t.Fatalf("%s worker = %q, want %q", fixture.Source, step.Worker, fixture.WantWorker)
				}
			case "normalize":
				got := normalizeObservation(fixture.Step, fixture.Classification, fixture.Session, fixture.Observation)
				if got.Status != fixture.WantStatus {
					t.Fatalf("%s status = %q, want %q", fixture.Source, got.Status, fixture.WantStatus)
				}
				if got.Outcome.Kind != fixture.WantOutcome {
					t.Fatalf("%s outcome = %q, want %q", fixture.Source, got.Outcome.Kind, fixture.WantOutcome)
				}
				if want := strings.TrimSpace(fixture.WantReasonContains); want != "" {
					if !strings.Contains(strings.ToLower(got.Outcome.Reason), strings.ToLower(want)) {
						t.Fatalf("%s outcome reason = %q, want substring %q", fixture.Source, got.Outcome.Reason, want)
					}
				}
				if want := strings.TrimSpace(fixture.WantSummaryContains); want != "" {
					if !strings.Contains(strings.ToLower(got.Summary), strings.ToLower(want)) {
						t.Fatalf("%s summary = %q, want substring %q", fixture.Source, got.Summary, want)
					}
				}
			default:
				t.Fatalf("unknown fixture kind %q", fixture.Kind)
			}
		})
	}
}

func TestParaphraseDirectoryInspectRoutesThroughReaderWorker(t *testing.T) {
	lines := loadParaphrases(t, filepath.Join("testdata", "paraphrases", "directory-inspect.txt"))
	for _, input := range lines {
		t.Run(input, func(t *testing.T) {
			class := Classify(UserTurn{Text: input}, SessionState{})
			if class.Family != FamilyInspect {
				t.Fatalf("family = %q", class.Family)
			}
			if class.WantsEvaluation {
				t.Fatal("unexpected evaluation request")
			}
			step := Plan(class, SessionState{})
			if step.Kind != StepWorker || step.Worker != WorkerReader {
				t.Fatalf("step = %#v", step)
			}
		})
	}
}

func TestParaphraseFollowUpsReuseOnlyRecentEvidence(t *testing.T) {
	lines := loadParaphrases(t, filepath.Join("testdata", "paraphrases", "follow-up-interpret.txt"))
	recent := SessionState{
		Turn: 2,
		LastEvidence: EvidenceSnapshot{
			Turn:     1,
			TopicKey: "workspace:directory",
			Summary:  "directory overview",
		},
	}
	expired := SessionState{
		Turn: 4,
		LastEvidence: EvidenceSnapshot{
			Turn:     1,
			TopicKey: "workspace:directory",
			Summary:  "directory overview",
		},
	}
	for _, input := range lines {
		t.Run("recent/"+input, func(t *testing.T) {
			class := Classify(UserTurn{Text: input}, recent)
			if !class.WantsInterpretation || !class.IsFollowUp {
				t.Fatalf("expected interpretive follow-up, got %#v", class)
			}
			if class.TopicKey != "workspace:directory" {
				t.Fatalf("topic = %q", class.TopicKey)
			}
		})
		t.Run("expired/"+input, func(t *testing.T) {
			class := Classify(UserTurn{Text: input}, expired)
			if class.WantsInterpretation {
				t.Fatalf("unexpected interpretive follow-up after evidence expired: %#v", class)
			}
		})
	}
}

func loadRegressionFixtures(t *testing.T, path string) []regressionFixture {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var fixtures []regressionFixture
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var fixture regressionFixture
		if err := json.Unmarshal([]byte(line), &fixture); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		fixtures = append(fixtures, fixture)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return fixtures
}

func loadParaphrases(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func loadPreviewBranchClaimRegressionFixtures(t *testing.T, path string) []previewBranchClaimRegressionFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var fixtures []previewBranchClaimRegressionFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures loaded from %s", path)
	}
	return fixtures
}
