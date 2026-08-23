package tools

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Hashline anchors let the model address a block of a file by two short
// content hashes instead of echoing the block back verbatim. read_file prints
// the anchor beside every line; edit_file accepts them in place of old_text.
//
// Anchors hash the line's content with trailing whitespace stripped, so an
// anchor survives trailing-space churn and is stable no matter where the line
// moves. Repeated lines share an anchor by design — the start/end pair, plus an
// optional line hint, is what makes a span unique.

func lineAnchor(line string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimRight(line, " \t\r")))
	sum := h.Sum32()
	return fmt.Sprintf("%04x", (sum^(sum>>16))&0xffff)
}

// renderHashlines formats lines[start-1:end] (1-indexed, inclusive) as
// "<lineno> <anchor> | <content>".
func renderHashlines(lines []string, start, end int) string {
	var sb strings.Builder
	for i := start; i <= end && i <= len(lines); i++ {
		fmt.Fprintf(&sb, "%4d %s | %s\n", i, lineAnchor(lines[i-1]), lines[i-1])
	}
	return sb.String()
}

type anchorSpan struct {
	start int // 1-indexed, inclusive
	end   int // 1-indexed, inclusive
}

// resolveAnchorSpan finds the single block bracketed by startAnchor and
// endAnchor. endAnchor may be empty for a single-line span. hintLine, when
// non-zero, selects the candidate starting on that line.
func resolveAnchorSpan(lines []string, startAnchor, endAnchor string, hintLine int) (anchorSpan, error) {
	startAnchor = strings.ToLower(strings.TrimSpace(startAnchor))
	endAnchor = strings.ToLower(strings.TrimSpace(endAnchor))
	if endAnchor == "" {
		endAnchor = startAnchor
	}

	anchors := make([]string, len(lines))
	for i, line := range lines {
		anchors[i] = lineAnchor(line)
	}

	var candidates []anchorSpan
	for i, a := range anchors {
		if a != startAnchor {
			continue
		}
		for j := i; j < len(anchors); j++ {
			if anchors[j] == endAnchor {
				candidates = append(candidates, anchorSpan{start: i + 1, end: j + 1})
				break
			}
		}
	}

	if len(candidates) == 0 {
		if !containsAnchor(anchors, startAnchor) {
			return anchorSpan{}, fmt.Errorf("start_anchor %s is stale — the file changed since it was read; read_file again for fresh anchors", startAnchor)
		}
		return anchorSpan{}, fmt.Errorf("end_anchor %s does not appear at or after start_anchor %s", endAnchor, startAnchor)
	}

	if hintLine > 0 {
		var filtered []anchorSpan
		for _, c := range candidates {
			if c.start == hintLine {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) == 0 {
			return anchorSpan{}, fmt.Errorf("no block starting with anchor %s on line %d (found on %s)", startAnchor, hintLine, startLineList(candidates))
		}
		candidates = filtered
	}

	if len(candidates) > 1 {
		return anchorSpan{}, fmt.Errorf("anchor %s is ambiguous — blocks start on lines %s; pass start_line to pick one", startAnchor, startLineList(candidates))
	}
	return candidates[0], nil
}

func containsAnchor(anchors []string, want string) bool {
	for _, a := range anchors {
		if a == want {
			return true
		}
	}
	return false
}

func startLineList(spans []anchorSpan) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		parts = append(parts, fmt.Sprint(s.start))
	}
	return strings.Join(parts, ", ")
}

// replaceSpan swaps lines[span.start-1:span.end] for newText, which may be
// empty to delete the block.
func replaceSpan(lines []string, span anchorSpan, newText string) []string {
	var replacement []string
	if newText != "" {
		replacement = strings.Split(strings.TrimSuffix(newText, "\n"), "\n")
	}
	out := make([]string, 0, len(lines)-(span.end-span.start+1)+len(replacement))
	out = append(out, lines[:span.start-1]...)
	out = append(out, replacement...)
	out = append(out, lines[span.end:]...)
	return out
}
