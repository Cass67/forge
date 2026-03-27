package harness

import "testing"

func TestRepoReviewPromptCorpusRoutesToEvaluativeInspect(t *testing.T) {
	prompts := repoReviewPromptCorpus()
	if len(prompts) < 100 {
		t.Fatalf("prompt corpus too small: got %d, want at least 100", len(prompts))
	}

	for _, input := range prompts {
		t.Run(input, func(t *testing.T) {
			class := Classify(UserTurn{Text: input}, SessionState{})
			if class.Family != FamilyInspect {
				t.Fatalf("family = %q", class.Family)
			}
			if !class.WantsEvaluation {
				t.Fatalf("expected evaluative inspect: %#v", class)
			}
			if class.WantsAction {
				t.Fatalf("unexpected action request: %#v", class)
			}
			if class.TopicKey != "workspace:repository" {
				t.Fatalf("topic = %q", class.TopicKey)
			}
			step := Plan(class, SessionState{})
			if step.Kind != StepLocal || step.Worker != WorkerNone {
				t.Fatalf("step = %#v", step)
			}
		})
	}
}

func repoReviewPromptCorpus() []string {
	leads := []string{
		"review this repo",
		"review the repo",
		"take a look at this repo",
		"take a look over this repo",
		"explain this repo",
		"go over this repository",
		"walk through this codebase",
		"help me understand this project",
		"reveiw this repo",
		"desribe this reposotory",
	}
	tails := []string{
		"and suggest improvements",
		"and suggest improvments",
		"and tell me what improvements could be made",
		"and tell me what improvments could be made",
		"and tell me what should change",
		"and tell me what you would change",
		"and tell me if there is anything you would change",
		"and tell me if there is anything you'd change",
		"and point out any problems",
		"and point out any problms",
		"and recommend cleanup actions",
		"and recommend clenaup actions",
		"and tell me whats happeingin and what improvments could be made",
	}
	prompts := make([]string, 0, len(leads)*len(tails))
	for _, lead := range leads {
		for _, tail := range tails {
			prompts = append(prompts, lead+" "+tail)
		}
	}
	return prompts
}
