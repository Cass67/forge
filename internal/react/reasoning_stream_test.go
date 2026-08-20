package react

import (
	"context"
	"strings"
	"sync"
	"testing"

	"forge/internal/llm"
)

// reasoningRenderer records thinking separately from the answer.
type reasoningRenderer struct {
	recordingRenderer
	mu        sync.Mutex
	reasoning []string
}

func (r *reasoningRenderer) AgentReasoning(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasoning = append(r.reasoning, text)
}

func (r *reasoningRenderer) reasoningText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.reasoning, "")
}

// reasoningDriver emits thinking, then a final answer.
type reasoningDriver struct{ reasoning string }

func (d *reasoningDriver) Name() string { return "reasoning" }

func (d *reasoningDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	for _, chunk := range strings.SplitAfter(d.reasoning, " ") {
		out <- llm.Token{ReasoningContent: chunk}
	}
	out <- llm.Token{Text: "Done."}
	return nil
}

// Thinking was captured and stored but never displayed, so a working turn
// showed nothing but tool cards.
func TestReasoningReachesTheRenderer(t *testing.T) {
	thinking := strings.Repeat("weighing the options here. ", 60)
	rec := &reasoningRenderer{}
	r := NewRunner(Config{
		Driver:        &reasoningDriver{reasoning: thinking},
		Session:       NewSession(),
		Renderer:      rec,
		ShowReasoning: true,
	})
	if err := r.Run(context.Background(), "think about it"); err != nil {
		t.Fatal(err)
	}
	if got := rec.reasoningText(); !strings.Contains(got, "weighing the options") {
		t.Fatalf("reasoning never reached the renderer: %q", got)
	}
}

// Reasoning is stored redacted, so it must be displayed redacted too.
func TestReasoningStreamDoesNotLeakSecrets(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("a", 40)
	thinking := strings.Repeat("checking config. ", 40) + secret + " " + strings.Repeat("and continuing. ", 60)

	rec := &reasoningRenderer{}
	r := NewRunner(Config{
		Driver:        &reasoningDriver{reasoning: thinking},
		Session:       NewSession(),
		Renderer:      rec,
		ShowReasoning: true,
	})
	if err := r.Run(context.Background(), "think about it"); err != nil {
		t.Fatal(err)
	}
	if got := rec.reasoningText(); strings.Contains(got, secret) {
		t.Fatalf("secret leaked to the reasoning display: %q", got)
	}
}

// Off by default for renderers that cannot show it, and when disabled.
func TestReasoningSuppressedWhenDisabled(t *testing.T) {
	rec := &reasoningRenderer{}
	r := NewRunner(Config{
		Driver:        &reasoningDriver{reasoning: strings.Repeat("thinking hard. ", 80)},
		Session:       NewSession(),
		Renderer:      rec,
		ShowReasoning: false,
	})
	if err := r.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := rec.reasoningText(); got != "" {
		t.Fatalf("reasoning shown while disabled: %q", got)
	}
}
