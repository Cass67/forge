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
}

func TestRegressionFixturesRouteWithoutEscalation(t *testing.T) {
	paths := []string{
		filepath.Join("testdata", "debuglogs", "repo-inspect-stall.jsonl"),
		filepath.Join("testdata", "debuglogs", "follow-up-misroute.jsonl"),
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
