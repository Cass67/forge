package permissions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"forge/internal/secscan"
)

func BuildClassifierPrompt(req ClassifierRequest) string {
	req = redactClassifierRequest(req)
	payload := map[string]any{
		"action":     req.Action,
		"risk":       req.Risk,
		"rules":      req.Rules,
		"transcript": strings.TrimSpace(req.Transcript),
		"output":     `{"decision":"allow|deny|ask","reason":"..."}`,
		"ruleset": []string{
			"Return ask for ambiguity.",
			"Never allow classifier-immune actions.",
			"Never allow secret-matched writes.",
			"Prefer allow for common tests, read-only git commands, and safe local build commands.",
		},
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return "Classify this pending Forge tool action. Return JSON only."
	}
	return "Classify this pending Forge tool action. Return JSON only: " + strings.TrimSpace(data.String())
}

func redactClassifierRequest(req ClassifierRequest) ClassifierRequest {
	req.Action.Summary = redactClassifierText(req.Action.Summary)
	req.Action.Detail = redactClassifierText(req.Action.Detail)
	req.Action.Path = redactClassifierText(req.Action.Path)
	req.Transcript = redactClassifierText(req.Transcript)
	if len(req.Rules) > 0 {
		rules := append([]Rule(nil), req.Rules...)
		for i := range rules {
			rules[i].Pattern = redactClassifierText(rules[i].Pattern)
		}
		req.Rules = rules
	}
	return req
}

func redactClassifierText(text string) string {
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func ParseClassifierResponse(text string) (ClassifierResponse, error) {
	var resp ClassifierResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &resp); err != nil {
		return ClassifierResponse{Decision: ClassifierAsk}, err
	}
	switch resp.Decision {
	case ClassifierAllow, ClassifierDeny, ClassifierAsk:
		return resp, nil
	default:
		return ClassifierResponse{Decision: ClassifierAsk}, fmt.Errorf("unknown classifier decision %q", resp.Decision)
	}
}
