package harness

import "testing"

func TestClassifyDirectoryParaphrasesStayInspect(t *testing.T) {
	cases := []string{
		"describe this directory",
		"go over this directory",
		"walk through this directory",
		"explain this directory",
		"review this directory",
		"summarize this directory",
		"give an overview of this directory",
		"take me through this directory",
		"show me what’s in this directory",
		"help me understand this directory",
	}

	for _, input := range cases {
		got := Classify(UserTurn{Text: input}, SessionState{})
		if got.Family != FamilyInspect {
			t.Fatalf("%q family = %q", input, got.Family)
		}
		if got.WantsEvaluation {
			t.Fatalf("%q unexpectedly wanted evaluation", input)
		}
		if got.WantsInterpretation {
			t.Fatalf("%q unexpectedly wanted interpretation", input)
		}
		if got.TopicKey != "workspace:directory" {
			t.Fatalf("%q topic = %q", input, got.TopicKey)
		}
	}
}

func TestClassifyInterpretiveFollowUpNeedsRecentEvidence(t *testing.T) {
	noEvidence := Classify(UserTurn{Text: "what do you think?"}, SessionState{})
	if noEvidence.WantsInterpretation {
		t.Fatal("expected no interpretation without recent evidence")
	}

	withEvidence := Classify(UserTurn{Text: "what do you think?"}, SessionState{
		Turn: 2,
		LastEvidence: EvidenceSnapshot{
			Turn:     1,
			TopicKey: "workspace:directory",
			Summary:  "read the directory",
		},
	})
	if !withEvidence.WantsInterpretation {
		t.Fatal("expected interpretation when recent evidence exists")
	}
	if !withEvidence.IsFollowUp {
		t.Fatal("expected follow-up classification")
	}
	if withEvidence.TopicKey != "workspace:directory" {
		t.Fatalf("topic = %q", withEvidence.TopicKey)
	}
}

func TestClassifyQuestionLikeFollowUpWithoutConcreteTargetStaysInspect(t *testing.T) {
	got := Classify(UserTurn{Text: "anything i need change?"}, SessionState{
		Turn: 2,
		LastEvidence: EvidenceSnapshot{
			Turn:     1,
			TopicKey: "workspace:repository",
			Summary:  "repo overview",
		},
	})
	if got.Family != FamilyInspect {
		t.Fatalf("family = %q", got.Family)
	}
	if !got.WantsEvaluation {
		t.Fatal("expected evaluation follow-up")
	}
	if got.WantsAction {
		t.Fatal("did not expect action request")
	}
	if !got.IsFollowUp {
		t.Fatal("expected follow-up classification")
	}
	if got.TopicKey != "workspace:repository" {
		t.Fatalf("topic = %q", got.TopicKey)
	}
}

func TestClassifyRecognizesImplementationAndDebugFamilies(t *testing.T) {
	if got := Classify(UserTurn{Text: "fix the broken auth flow"}, SessionState{}); got.Family != FamilyImplement {
		t.Fatalf("fix request family = %q", got.Family)
	}
	if got := Classify(UserTurn{Text: "debug the failing auth flow"}, SessionState{}); got.Family != FamilyDebug {
		t.Fatalf("debug request family = %q", got.Family)
	}
}

func TestClassifyResearchNeedsExplicitExternalLookupSignals(t *testing.T) {
	cases := []struct {
		input string
		want  RequestFamily
	}{
		{input: "look at this file", want: FamilyAnswer},
		{input: "search the repo for auth", want: FamilyInspect},
		{input: "update docs for the auth flow", want: FamilyImplement},
		{input: "look up the latest API docs", want: FamilyResearch},
		{input: "search the web for the latest API docs", want: FamilyResearch},
	}

	for _, tc := range cases {
		got := Classify(UserTurn{Text: tc.input}, SessionState{})
		if got.Family != tc.want {
			t.Fatalf("%q family = %q, want %q", tc.input, got.Family, tc.want)
		}
	}
}
