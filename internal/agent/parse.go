package agent

import (
	"encoding/json"
	"strings"
)

type ToolCall struct {
	Name string
	Args map[string]any
}

func ParseToolCalls(text string) ([]ToolCall, string) {
	var calls []ToolCall
	var textParts []string

	lines := strings.Split(text, "\n")
	i := 0
	inCodeFence := false

	for i < len(lines) {
		line := lines[i]

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeFence {
				inCodeFence = false
				textParts = append(textParts, line)
				i++
				continue
			}
			inCodeFence = true
			textParts = append(textParts, line)
			i++
			continue
		}

		if inCodeFence {
			textParts = append(textParts, line)
			i++
			continue
		}

		lineTrimmed := strings.TrimSpace(line)
		if openTag, closeTag, ok := toolCallBlockTags(lineTrimmed); ok {
			after := strings.SplitN(lineTrimmed, openTag, 2)[1]
			after = strings.TrimSpace(after)
			i++
			var block strings.Builder
			if after != "" {
				if strings.Contains(after, closeTag) {
					before := strings.SplitN(after, closeTag, 2)[0]
					before = strings.TrimSpace(before)
					if before != "" {
						block.WriteString(before)
						block.WriteByte('\n')
					}
					calls = append(calls, parseCallsJSON(block.String())...)
					continue
				}
				block.WriteString(after)
				block.WriteByte('\n')
			}
			for i < len(lines) {
				lt := strings.TrimSpace(lines[i])
				if strings.Contains(lt, closeTag) {
					before := strings.SplitN(lt, closeTag, 2)[0]
					before = strings.TrimSpace(before)
					if before != "" {
						block.WriteString(before)
						block.WriteByte('\n')
					}
					i++
					break
				}
				block.WriteString(lines[i])
				block.WriteByte('\n')
				i++
			}

			calls = append(calls, parseCallsJSON(block.String())...)
			continue
		}

		textParts = append(textParts, line)
		i++
	}

	return calls, strings.Join(textParts, "\n")
}

func toolCallBlockTags(line string) (openTag, closeTag string, ok bool) {
	switch {
	case strings.Contains(line, "<tool_call>"):
		return "<tool_call>", "</tool_call>", true
	case strings.Contains(line, "<function_calls>"):
		return "<function_calls>", "</function_calls>", true
	default:
		return "", "", false
	}
}

func parseCallsJSON(raw string) []ToolCall {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var many []struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(raw), &many); err == nil {
		out := make([]ToolCall, 0, len(many))
		for _, parsed := range many {
			if parsed.Name == "" {
				continue
			}
			if parsed.Args == nil {
				parsed.Args = make(map[string]any)
			}
			out = append(out, ToolCall{Name: parsed.Name, Args: parsed.Args})
		}
		return out
	}

	var parsed struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	if parsed.Name == "" {
		return nil
	}
	if parsed.Args == nil {
		parsed.Args = make(map[string]any)
	}
	return []ToolCall{{Name: parsed.Name, Args: parsed.Args}}
}
