package bootstrap

import "strings"

type ModelRef struct {
	Provider string
	Model    string
	Raw      string
}

func ParseModelRef(raw string) ModelRef {
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) == 2 && isProviderName(parts[0]) {
		return ModelRef{Provider: parts[0], Model: parts[1], Raw: raw}
	}
	return ModelRef{Model: raw, Raw: raw}
}

func ResolveCompatProvider(cfgProvider []CompatProvider, model string) (*CompatProvider, bool) {
	ref := ParseModelRef(model)
	if ref.Provider != "" {
		for i := range cfgProvider {
			p := &cfgProvider[i]
			if p.Name == ref.Provider && p.KeyFn() != "" {
				return p, true
			}
		}
		return nil, true
	}

	matches := make([]*CompatProvider, 0)
	for i := range cfgProvider {
		p := &cfgProvider[i]
		if p.KeyFn() != "" && p.IsModel(model) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], false
	}
	return nil, len(matches) > 1
}

func QualifyModel(ref ModelRef) string {
	if ref.Provider == "" {
		return ref.Model
	}
	return ref.Provider + "/" + ref.Model
}

func isProviderName(name string) bool {
	switch name {
	case "anthropic", "claude", "openai", "chatgpt", "copilot", "xai", "mistral", "perplexity", "cerebras", "groq", "nvidia", "together", "deepinfra", "openrouter":
		return true
	default:
		return false
	}
}
