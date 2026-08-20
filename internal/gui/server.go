package gui

import (
	"encoding/json"
	"log"

	"forge/internal/llm"
	"forge/internal/skills"
	"forge/internal/tui"
)

// server bridges the runtime event stream to connected browser tabs.
type server struct {
	cfg     tui.ChatLiveConfig
	inputCh chan<- string

	hub   *hub
	outCh chan []byte // single-writer queue; the writer goroutine broadcasts
}

func newServer(cfg tui.ChatLiveConfig, inputCh chan<- string) *server {
	return &server{
		cfg:     cfg,
		inputCh: inputCh,
		hub:     newHub(),
		outCh:   make(chan []byte, 1024),
	}
}

func (s *server) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("gui: marshal frame: %v", err)
		return
	}
	select {
	case s.outCh <- b:
	default:
		log.Printf("gui: dropping frame (client read too slow): %s", b[:min(len(b), 120)])
	}
}

func (s *server) sendInit() {
	s.send(initFrame{
		Type:        "init",
		Model:       s.cfg.Model,
		WorkDir:     s.cfg.WorkDir,
		Models:      s.cfg.AvailableModels,
		Providers:   providerFrames(s.cfg.Providers),
		Effort:      currentEffort(s.cfg),
		Efforts:     modelEfforts(s.cfg, s.cfg.Model),
		Skills:      skillFrames(s.cfg.Skills),
		ThreadID:    currentThreadID(s.cfg),
		RequestMode: requestMode(s.cfg),
	})
}

func currentEffort(cfg tui.ChatLiveConfig) string {
	if cfg.CurrentEffort != nil {
		return cfg.CurrentEffort()
	}
	return ""
}

func modelEfforts(cfg tui.ChatLiveConfig, model string) []string {
	if cfg.ModelEfforts != nil {
		return cfg.ModelEfforts(model)
	}
	return nil
}

func currentThreadID(cfg tui.ChatLiveConfig) string {
	if cfg.CurrentThreadID != nil {
		return cfg.CurrentThreadID()
	}
	return ""
}

func requestMode(cfg tui.ChatLiveConfig) string {
	if cfg.RequestMode != nil {
		return cfg.RequestMode()
	}
	return ""
}

func providerFrames(in []tui.ProviderOption) []providerFrame {
	out := make([]providerFrame, 0, len(in))
	for _, p := range in {
		out = append(out, providerFrame{
			ID: p.ID, Label: p.Label, Status: p.Status, DefaultModel: p.DefaultModel,
		})
	}
	return out
}

func skillFrames(in []skills.Skill) []skillFrame {
	out := make([]skillFrame, 0, len(in))
	for _, sk := range in {
		out = append(out, skillFrame{Name: sk.Name, Description: sk.Description})
	}
	return out
}

var _ = llm.EventDone // keep llm import for pumps
