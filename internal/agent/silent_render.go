package agent

import (
	"time"

	"forge/internal/llm"
)

type discardRenderTarget struct{}

func NewSilentRenderer(base RenderTarget) RenderTarget {
	_ = base
	return discardRenderTarget{}
}

func (discardRenderTarget) AgentToken(string)                       {}
func (discardRenderTarget) AgentText(string)                        {}
func (discardRenderTarget) ToolCall(string, string)                 {}
func (discardRenderTarget) ToolResult(string, string, string, bool) {}
func (discardRenderTarget) Stats(time.Duration, llm.Usage)          {}
func (discardRenderTarget) Error(string)                            {}
func (discardRenderTarget) Info(string)                             {}
func (discardRenderTarget) Progress(string)                         {}
