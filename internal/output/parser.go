package output

import (
	"path/filepath"
	"strings"
)

// CodeBlock is a parsed fenced code block with a filename annotation.
type CodeBlock struct {
	Filename string // relative path, e.g. "internal/limiter/limiter.go"
	Content  string
}

// ParseCodeBlocks extracts fenced code blocks with filename annotations from LLM output.
// Format: ```<lang>:<filename>\n<content>\n```
// Blocks without a filename annotation are ignored.
// Filenames containing ".." are rejected to prevent path traversal.
func ParseCodeBlocks(text string) []CodeBlock {
	var blocks []CodeBlock
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "```") {
			i++
			continue
		}
		header := strings.TrimPrefix(line, "```")
		// expect format: <lang>:<filename>
		colon := strings.Index(header, ":")
		if colon < 0 {
			i++
			continue
		}
		filename := header[colon+1:]
		if filename == "" || strings.Contains(filename, "..") {
			i++
			continue
		}
		// sanitise: must be relative
		clean := filepath.Clean(filename)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			i++
			continue
		}
		// collect content until closing ```
		i++
		var content strings.Builder
		for i < len(lines) && lines[i] != "```" {
			content.WriteString(lines[i])
			content.WriteByte('\n')
			i++
		}
		i++ // skip closing ```
		blocks = append(blocks, CodeBlock{Filename: clean, Content: content.String()})
	}
	return blocks
}
