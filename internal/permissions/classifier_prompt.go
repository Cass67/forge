package permissions

import (
	"encoding/json"
	"fmt"
	"strings"
)

func BuildClassifierPrompt(req ClassifierRequest) string {
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
	data, err := json.Marshal(payload)
	if err != nil {
		return "Classify this pending Forge tool action. Return JSON only."
	}
	return "Classify this pending Forge tool action. Return JSON only: " + string(data)
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
