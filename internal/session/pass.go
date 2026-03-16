package session

// Pass describes one iteration phase in the forge pipeline.
type Pass struct {
    Name         string
    Rounds       int    // number of writer→auditor rounds to run
    PromptFile   string // embedded prompt file name (in prompts/)
    OverridePath string // optional: user-provided prompt override path
}

// ShouldContinue is the convergence hook. In v1 it always returns true.
func (p *Pass) ShouldContinue(round int, _ string) bool {
    return true
}

// DefaultPasses returns the four built-in forge passes in order.
func DefaultPasses(rounds int) []Pass {
    return []Pass{
        {Name: "correctness", Rounds: rounds, PromptFile: "correctness.md"},
        {Name: "refactor", Rounds: rounds, PromptFile: "refactor.md"},
        {Name: "security", Rounds: rounds, PromptFile: "security.md"},
        {Name: "prod", Rounds: rounds, PromptFile: "prod.md"},
    }
}
