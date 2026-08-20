package gui

import (
	"encoding/json"
	"strings"

	"forge/internal/tui"
)

func (s *server) handleClientFrame(data []byte) {
	var f clientFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	switch f.Type {
	case "input":
		if strings.TrimSpace(f.Text) != "" {
			s.inputCh <- f.Text
		}
	case "approve":
		if s.cfg.ResponseCh != nil {
			s.cfg.ResponseCh <- f.OK
		}
	case "action":
		s.handleAction(f)
	}
}

func (s *server) result(name string, ok bool, payload any, err error) {
	raw, _ := json.Marshal(payload)
	s.send(actionResultFrame{
		Type: "action_result", Name: name, OK: ok,
		Payload: raw, Error: errStr(err),
	})
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *server) handleAction(f clientFrame) {
	cfg := s.cfg
	switch f.Name {
	case "switch_model":
		var p struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		model, err := cfg.SwitchModel(p.Model)
		s.result(f.Name, err == nil, struct {
			Model string `json:"model"`
		}{Model: model}, err)
	case "effort":
		var p struct {
			Effort string `json:"effort"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		err := error(nil)
		if cfg.SetEffort != nil {
			err = cfg.SetEffort(p.Effort)
		}
		s.result(f.Name, err == nil, nil, err)
	case "efforts":
		var p struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		efforts := []string(nil)
		if cfg.ModelEfforts != nil {
			efforts = cfg.ModelEfforts(p.Model)
		}
		s.result(f.Name, true, struct {
			Efforts []string `json:"efforts"`
		}{Efforts: efforts}, nil)
	case "models":
		models := cfg.AvailableModels
		if cfg.RefreshModels != nil {
			models = cfg.RefreshModels()
		}
		s.result(f.Name, true, struct {
			Models []string `json:"models"`
		}{Models: models}, nil)
	case "clear":
		if cfg.ClearHistory != nil {
			cfg.ClearHistory()
		}
		s.result(f.Name, true, nil, nil)
	case "new_session":
		s.inputCh <- "__new_session__"
		s.result(f.Name, true, nil, nil)
	case "cancel":
		s.inputCh <- "__cancel_turn__"
		s.result(f.Name, true, nil, nil)
	case "threads":
		items := []tui.ThreadSummary(nil)
		if cfg.ListThreads != nil {
			items = cfg.ListThreads()
		}
		s.send(threadsFrame{Type: "threads", Items: items})
	case "history":
		var p struct {
			ThreadID string `json:"thread_id"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		s.send(historyFrame{Type: "history", ThreadID: p.ThreadID, Items: readItems(cfg.ReadThreadItems, p.ThreadID)})
	case "restore":
		var p struct {
			ThreadID string `json:"thread_id"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		if cfg.RestoreHistory == nil {
			s.result(f.Name, false, nil, errNoRestore)
			return
		}
		n, err := cfg.RestoreHistory(p.ThreadID)
		s.send(historyFrame{Type: "history", ThreadID: p.ThreadID, Items: readItems(cfg.ReadThreadItems, p.ThreadID), Restored: n})
		s.result(f.Name, err == nil, struct {
			Restored int `json:"restored"`
		}{Restored: n}, err)
	case "status":
		s.result(f.Name, true, struct {
			ThreadID string `json:"thread_id"`
			Model    string `json:"model"`
		}{
			ThreadID: currentThreadID(cfg),
			Model:    cfg.Model,
		}, nil)
	default:
		s.result(f.Name, false, nil, errUnknownAction)
	}
}
