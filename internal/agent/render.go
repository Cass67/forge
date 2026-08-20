package agent

import (
	"fmt"
	"io"
	"strings"
	"time"

	"forge/internal/agent/format"
	"forge/internal/llm"
)

// RenderTarget is the interface the agent uses for output.
type RenderTarget interface {
	AgentToken(text string)
	AgentText(text string)
	ToolCall(name, summary string)
	ToolResult(name, output, diff string, isError bool)
	Stats(duration time.Duration, usage llm.Usage)
	Error(msg string)
	Info(msg string)
}

type ContextStatsTarget interface {
	StatsWithContext(duration time.Duration, usage llm.Usage, contextUsed, contextLimit int)
}

// ReasoningTarget is optionally implemented by renderers that can show the
// model's thinking while it works. Reasoning was already captured and stored
// on the session; without this seam nothing could display it.
type ReasoningTarget interface {
	AgentReasoning(text string)
}

// RetryNotifier is optionally implemented by renderers that can explicitly
// retract or reset a provisional assistant draft before the next attempt.
type RetryNotifier interface {
	Retry(msg string)
}

type Renderer struct {
	out    io.Writer
	width  int
	colors bool
}

func NewRenderer(out io.Writer, width int, colors bool) *Renderer {
	return &Renderer{out: out, width: width, colors: colors}
}

func (r *Renderer) AgentToken(text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i < len(lines)-1 {
			styled := format.AgentLine(line)
			fmt.Fprintln(r.out, format.LineToANSI(styled, r.colors))
		} else if line != "" {
			styled := format.AgentLine(line)
			fmt.Fprint(r.out, format.LineToANSI(styled, r.colors))
		}
	}
}

func (r *Renderer) AgentText(text string) {
	r.AgentToken(text)
}

func (r *Renderer) ToolCall(name, summary string) {
	ts := format.ToolStyle(name)
	line := format.Line{Spans: []format.Span{
		{Text: "\n ● ", Style: ts},
		{Text: name, Style: ts},
	}}
	if summary != "" {
		line.Spans = append(line.Spans, format.Span{Text: "  " + summary, Style: format.StyleDim})
	}
	line.Spans = append(line.Spans, format.Span{Text: " ...", Style: format.StyleDim})
	fmt.Fprintln(r.out, format.LineToANSI(line, r.colors))
}

func (r *Renderer) ToolResult(name, output, diff string, isError bool) {
	status := format.StatusSuccess
	if isError {
		status = format.StatusError
	}

	detail := diff
	if detail == "" && isError {
		detail = output
	}

	displayDetail := detail
	if detail != "" {
		truncated, wasTruncated := format.Truncate(detail, 20)
		if wasTruncated {
			displayDetail = truncated
		}
	}

	lines := format.ToolBox(name, output, displayDetail, status, r.width)
	fmt.Fprintln(r.out, format.ToANSI(lines, r.colors))
}

func (r *Renderer) Stats(duration time.Duration, usage llm.Usage) {
	line := format.Stats(duration, usage.InputTokens, usage.OutputTokens)
	fmt.Fprintln(r.out, format.LineToANSI(line, r.colors))
}

func (r *Renderer) Error(msg string) {
	line := format.Line{Spans: []format.Span{
		{Text: " ✗ " + msg, Style: format.StyleError},
	}}
	fmt.Fprintln(r.out, format.LineToANSI(line, r.colors))
}

func (r *Renderer) Info(msg string) {
	line := format.Line{Spans: []format.Span{
		{Text: " ● " + msg, Style: format.StyleDim},
	}}
	fmt.Fprintln(r.out, format.LineToANSI(line, r.colors))
}

func (r *Renderer) Retry(msg string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	fmt.Fprintln(r.out)
	r.Info(msg)
}

func (r *Renderer) Progress(msg string) {
	r.Info(msg)
}

func (r *Renderer) Prompt() {
	if r.colors {
		fmt.Fprintf(r.out, "\n\033[1;32mforge>\033[0m ")
	} else {
		fmt.Fprint(r.out, "\nforge> ")
	}
}
