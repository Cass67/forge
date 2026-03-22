package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

func (m *chatLiveModel) openSearchOverlay() {
	if m.panes.focusR {
		m.overlays.search.pane = "right"
	} else {
		m.overlays.search.pane = "left"
	}
	m.overlays.search.visible = true
	m.overlays.search.pos = len([]rune(m.overlays.search.query))
	m.updateSearchMatches(false)
}

func (m *chatLiveModel) handleSearchOverlayKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.overlays.search.visible = false
	case tcell.KeyEnter:
		m.updateSearchMatches(true)
		m.overlays.search.visible = false
	case tcell.KeyLeft:
		if m.overlays.search.pos > 0 {
			m.overlays.search.pos--
		}
	case tcell.KeyRight:
		if m.overlays.search.pos < len([]rune(m.overlays.search.query)) {
			m.overlays.search.pos++
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if m.overlays.search.pos > 0 {
			runes := []rune(m.overlays.search.query)
			m.overlays.search.query = string(runes[:m.overlays.search.pos-1]) + string(runes[m.overlays.search.pos:])
			m.overlays.search.pos--
			m.updateSearchMatches(false)
		}
	case tcell.KeyDelete:
		runes := []rune(m.overlays.search.query)
		if m.overlays.search.pos < len(runes) {
			m.overlays.search.query = string(runes[:m.overlays.search.pos]) + string(runes[m.overlays.search.pos+1:])
			m.updateSearchMatches(false)
		}
	case tcell.KeyCtrlA:
		m.overlays.search.pos = 0
	case tcell.KeyCtrlE:
		m.overlays.search.pos = len([]rune(m.overlays.search.query))
	case tcell.KeyCtrlU:
		m.overlays.search.query = ""
		m.overlays.search.pos = 0
		m.updateSearchMatches(false)
	case tcell.KeyRune:
		runes := []rune(m.overlays.search.query)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.overlays.search.pos]...)
		newRunes = append(newRunes, ev.Rune())
		newRunes = append(newRunes, runes[m.overlays.search.pos:]...)
		m.overlays.search.query = string(newRunes)
		m.overlays.search.pos++
		m.updateSearchMatches(false)
	}
	return ChatLiveResult{}
}

func (m *chatLiveModel) openModelPicker() {
	m.overlays.models.visible = true
	m.overlays.models.cursor = 0
	for i, name := range m.overlays.models.list {
		if name == m.model {
			m.overlays.models.cursor = i
			break
		}
	}
}

func (m *chatLiveModel) handleHelpOverlayKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		m.overlays.helpVisible = false
	case tcell.KeyRune:
		if ev.Rune() == '?' || ev.Rune() == 'q' || ev.Rune() == 'Q' {
			m.overlays.helpVisible = false
		}
	}
	return ChatLiveResult{}
}

func (m *chatLiveModel) handleModelPickerKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.overlays.models.visible = false
	case tcell.KeyUp:
		if m.overlays.models.cursor > 0 {
			m.overlays.models.cursor--
		}
	case tcell.KeyDown:
		if m.overlays.models.cursor < len(m.overlays.models.list)-1 {
			m.overlays.models.cursor++
		}
	case tcell.KeyEnter:
		m.pickModel(m.overlays.models.cursor)
	case tcell.KeyRune:
		r := ev.Rune()
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.overlays.models.list) {
				m.overlays.models.cursor = idx
				m.pickModel(idx)
			}
		}
	}
	return ChatLiveResult{}
}

func resolveModelName(models []string, input string) string {
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1]
	}
	for _, m := range models {
		if strings.EqualFold(m, input) {
			return m
		}
	}
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), strings.ToLower(input)) {
			return m
		}
	}
	return ""
}

func (m *chatLiveModel) openSessionsPicker() {
	sessions, err := listChatSessions()
	if err != nil {
		m.display.flash = fmt.Sprintf("sessions failed: %v", err)
		return
	}
	if len(sessions) == 0 {
		m.display.flash = "no saved sessions"
		return
	}
	m.overlays.sessions.list = sessions
	m.overlays.sessions.visible = true
	m.overlays.sessions.rename.active = false
	m.overlays.sessions.cursor = 0
	for i, session := range sessions {
		if session.name == "last-session" {
			m.overlays.sessions.cursor = i
			break
		}
	}
}

func (m *chatLiveModel) handleSessionsPickerKey(ev *tcell.EventKey) ChatLiveResult {
	if m.overlays.sessions.rename.active {
		return m.handleSessionRenameKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		m.overlays.sessions.visible = false
	case tcell.KeyUp:
		if m.overlays.sessions.cursor > 0 {
			m.overlays.sessions.cursor--
		}
	case tcell.KeyDown:
		if m.overlays.sessions.cursor < len(m.overlays.sessions.list)-1 {
			m.overlays.sessions.cursor++
		}
	case tcell.KeyEnter:
		m.restorePickedSession(m.overlays.sessions.cursor)
	case tcell.KeyRune:
		r := ev.Rune()
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.overlays.sessions.list) {
				m.overlays.sessions.cursor = idx
				m.restorePickedSession(idx)
				return ChatLiveResult{}
			}
		}
		switch r {
		case 'd', 'D':
			m.deletePickedSession(m.overlays.sessions.cursor)
		case 'r', 'R':
			m.beginRenamePickedSession(m.overlays.sessions.cursor)
		}
	}
	return ChatLiveResult{}
}

func (m *chatLiveModel) restorePickedSession(idx int) {
	if idx < 0 || idx >= len(m.overlays.sessions.list) {
		return
	}
	name := m.overlays.sessions.list[idx].name
	if err := m.restoreSession(name); err != nil {
		m.display.flash = fmt.Sprintf("restore failed: %v", err)
		return
	}
	m.overlays.sessions.visible = false
	m.display.flash = fmt.Sprintf("session restored: %s", name)
}

func (m *chatLiveModel) beginRenamePickedSession(idx int) {
	if idx < 0 || idx >= len(m.overlays.sessions.list) {
		return
	}
	name := m.overlays.sessions.list[idx].name
	if name == "last-session" {
		m.display.flash = "cannot rename last-session"
		return
	}
	m.overlays.sessions.rename.active = true
	m.overlays.sessions.rename.buf = name
	m.overlays.sessions.rename.pos = len([]rune(name))
}

func (m *chatLiveModel) handleSessionRenameKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.overlays.sessions.rename.active = false
	case tcell.KeyEnter:
		m.commitRenamePickedSession()
	case tcell.KeyLeft:
		if m.overlays.sessions.rename.pos > 0 {
			m.overlays.sessions.rename.pos--
		}
	case tcell.KeyRight:
		if m.overlays.sessions.rename.pos < len([]rune(m.overlays.sessions.rename.buf)) {
			m.overlays.sessions.rename.pos++
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if m.overlays.sessions.rename.pos > 0 {
			runes := []rune(m.overlays.sessions.rename.buf)
			m.overlays.sessions.rename.buf = string(runes[:m.overlays.sessions.rename.pos-1]) + string(runes[m.overlays.sessions.rename.pos:])
			m.overlays.sessions.rename.pos--
		}
	case tcell.KeyDelete:
		runes := []rune(m.overlays.sessions.rename.buf)
		if m.overlays.sessions.rename.pos < len(runes) {
			m.overlays.sessions.rename.buf = string(runes[:m.overlays.sessions.rename.pos]) + string(runes[m.overlays.sessions.rename.pos+1:])
		}
	case tcell.KeyCtrlA:
		m.overlays.sessions.rename.pos = 0
	case tcell.KeyCtrlE:
		m.overlays.sessions.rename.pos = len([]rune(m.overlays.sessions.rename.buf))
	case tcell.KeyCtrlU:
		m.overlays.sessions.rename.buf = ""
		m.overlays.sessions.rename.pos = 0
	case tcell.KeyRune:
		runes := []rune(m.overlays.sessions.rename.buf)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.overlays.sessions.rename.pos]...)
		newRunes = append(newRunes, ev.Rune())
		newRunes = append(newRunes, runes[m.overlays.sessions.rename.pos:]...)
		m.overlays.sessions.rename.buf = string(newRunes)
		m.overlays.sessions.rename.pos++
	}
	return ChatLiveResult{}
}

func (m *chatLiveModel) commitRenamePickedSession() {
	if m.overlays.sessions.cursor < 0 || m.overlays.sessions.cursor >= len(m.overlays.sessions.list) {
		m.overlays.sessions.rename.active = false
		return
	}
	oldName := m.overlays.sessions.list[m.overlays.sessions.cursor].name
	newName := sanitizeChatSessionName(m.overlays.sessions.rename.buf)
	if newName == "" {
		m.display.flash = "rename failed: missing session name"
		return
	}
	if oldName == newName {
		m.overlays.sessions.rename.active = false
		return
	}
	if err := renameChatSession(oldName, newName); err != nil {
		m.display.flash = fmt.Sprintf("rename failed: %v", err)
		return
	}
	m.overlays.sessions.rename.active = false
	m.display.flash = fmt.Sprintf("session renamed: %s → %s", oldName, newName)
	m.openSessionsPicker()
}

func (m *chatLiveModel) deletePickedSession(idx int) {
	if idx < 0 || idx >= len(m.overlays.sessions.list) {
		return
	}
	name := m.overlays.sessions.list[idx].name
	if name == "last-session" {
		m.display.flash = "cannot delete last-session"
		return
	}
	if err := deleteChatSession(name); err != nil {
		m.display.flash = fmt.Sprintf("delete failed: %v", err)
		return
	}
	m.display.flash = fmt.Sprintf("session deleted: %s", name)
	m.openSessionsPicker()
}

func (m *chatLiveModel) pickModel(idx int) {
	if idx < 0 || idx >= len(m.overlays.models.list) {
		return
	}
	picked := m.overlays.models.list[idx]
	if m.switchModelFn != nil {
		newModel, err := m.switchModelFn(picked)
		if err != nil {
			m.display.flash = fmt.Sprintf("error: %v", err)
		} else {
			m.model = newModel
			m.display.flash = fmt.Sprintf("switched to %s", newModel)
		}
	}
	m.overlays.models.visible = false
}
