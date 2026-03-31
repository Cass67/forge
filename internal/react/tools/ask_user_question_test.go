package reacttools

import (
	"context"
	"strings"
	"testing"
)

func TestAskUserQuestionFormatsStructuredQuestion(t *testing.T) {
	tool := NewAskUserQuestion()
	result, err := tool.Execute(context.Background(), map[string]any{
		"question":     "Which implementation path should we take?",
		"options_json": `[{"label":"Prompt composer first","description":"Build composition before more tools"},{"label":"Plan mode first","description":"Prioritize explicit workflows"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Question: Which implementation path should we take?",
		"1. Prompt composer first",
		"2. Plan mode first",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
}
