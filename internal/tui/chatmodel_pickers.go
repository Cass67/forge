package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/claudeauth"
	"forge/internal/copilot"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *ChatModel) openSearchOverlay(query string) {
	m.searchPane = m.paneFocus
	m.searchVisible = true
	m.searchQuery = query
	m.searchPos = len([]rune(query))
	m.updateSearchMatches(false)
}

func (m *ChatModel) searchTarget() ([]string, *int, int, string) {
	switch m.searchPane {
	case focusTools:
		lines := m.toolsWrappedLines()
		if len(lines) == 0 {
			lines = []string{""}
		}
		return lines, &m.toolsScroll, max(1, m.chatViewport.Height), "tools"
	default:
		lines := strings.Split(m.chatContent, "\n")
		if len(lines) == 0 {
			lines = []string{""}
		}
		return lines, &m.chatViewport.YOffset, max(1, m.chatViewport.Height), "agent"
	}
}

func (m *ChatModel) updateSearchMatches(jump bool) {
	query := strings.TrimSpace(strings.ToLower(m.searchQuery))
	m.searchMatches = nil
	m.searchCurrent = -1
	if query == "" {
		return
	}
	lines, scroll, visible, paneName := m.searchTarget()
	for idx, line := range lines {
		if strings.Contains(strings.ToLower(line), query) {
			m.searchMatches = append(m.searchMatches, idx)
		}
	}
	if len(m.searchMatches) == 0 {
		m.flash = fmt.Sprintf("no matches for %q", m.searchQuery)
		return
	}
	m.searchCurrent = 0
	line := m.searchMatches[0]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	m.flash = fmt.Sprintf("%d match(es) in %s pane", len(m.searchMatches), paneName)
	if jump && len(m.searchMatches) > 1 {
		m.searchNext(1)
	}
}

func (m *ChatModel) openFilePicker(query string) {
	list, err := discoverContextFiles(m.workDir)
	if err != nil {
		m.flash = fmt.Sprintf("file picker failed: %v", err)
		return
	}
	m.filesVisible = true
	m.filesBrowser = false
	m.filesViewing = false
	m.filesList = list
	m.filesQuery = query
	m.filesPos = len([]rune(query))
	m.filesCursor = 0
	m.filesViewPath = ""
	m.filesViewText = ""
	m.filesViewScroll = 0
	m.updateFilePickerMatches()
}

func (m *ChatModel) openWorkspaceFileBrowser(query string) {
	m.openFilePicker(query)
	m.filesBrowser = true
	m.flash = "files opened"
}

func (m *ChatModel) updateFilePickerMatches() {
	query := strings.TrimSpace(strings.ToLower(m.filesQuery))
	filtered := make([]string, 0, len(m.filesList))
	for _, path := range m.filesList {
		if query == "" || strings.Contains(strings.ToLower(path), query) {
			filtered = append(filtered, path)
		}
	}
	m.filesFiltered = filtered
	if len(filtered) == 0 {
		m.filesCursor = 0
		return
	}
	m.filesCursor = clamp(m.filesCursor, 0, len(filtered)-1)
}

func (m *ChatModel) replaceActiveAtToken(path string) {
	runes := []rune(m.inputBuf)
	if m.inputPos < 0 || m.inputPos > len(runes) {
		return
	}
	start := m.inputPos
	for start > 0 {
		r := runes[start-1]
		if r == '@' {
			start--
			break
		}
		if r == ' ' || r == '\t' || r == '\n' {
			return
		}
		start--
	}
	if start < 0 || start >= len(runes) || runes[start] != '@' {
		return
	}
	end := m.inputPos
	for end < len(runes) {
		r := runes[end]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		end++
	}
	repl := "@" + path + " "
	prefix := string(runes[:start])
	m.inputBuf = prefix + repl + string(runes[end:])
	m.inputPos = len([]rune(prefix + repl))
	already := false
	for _, existing := range m.contextFiles {
		if existing == path {
			already = true
			break
		}
	}
	if !already {
		m.contextFiles = append(m.contextFiles, path)
		sort.Strings(m.contextFiles)
	}
	m.filesVisible = false
	m.filesViewing = false
	m.flash = fmt.Sprintf("added context %s", path)
}

func (m *ChatModel) addContextFile(path string) {
	if path == "" {
		return
	}
	already := false
	for _, existing := range m.contextFiles {
		if existing == path {
			already = true
			break
		}
	}
	if !already {
		m.contextFiles = append(m.contextFiles, path)
		sort.Strings(m.contextFiles)
	}
	m.flash = fmt.Sprintf("added context %s", path)
}

func (m *ChatModel) openFileViewer(path string) {
	resolved, err := tools.ResolvePath(m.workDir, path)
	if err != nil {
		m.flash = fmt.Sprintf("open failed: %v", err)
		return
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		m.filesViewText = fmt.Sprintf("error reading file: %v", err)
	} else if tools.IsBinary(data) {
		m.filesViewText = "binary file preview unavailable"
	} else {
		m.filesViewText = string(data)
	}
	m.filesViewing = true
	m.filesViewPath = path
	m.filesViewScroll = 0
}

func (m ChatModel) handleFilePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filesViewing {
		return m.handleFileViewerKey(msg)
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.filesVisible = false
	case tea.KeyEnter:
		if m.filesBrowser {
			if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
				m.openFileViewer(m.filesFiltered[m.filesCursor])
			}
			return m, nil
		}
		if rel := m.resolveExplicitContextPath(strings.TrimSpace(m.filesQuery)); rel != "" {
			m.replaceActiveAtToken(rel)
			return m, nil
		}
		if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
			m.replaceActiveAtToken(m.filesFiltered[m.filesCursor])
		}
	case tea.KeyUp:
		if m.filesCursor > 0 {
			m.filesCursor--
		}
	case tea.KeyDown:
		if m.filesCursor < len(m.filesFiltered)-1 {
			m.filesCursor++
		}
	case tea.KeyLeft:
		if m.filesPos > 0 {
			m.filesPos--
		}
	case tea.KeyRight:
		if m.filesPos < len([]rune(m.filesQuery)) {
			m.filesPos++
		}
	case tea.KeyBackspace:
		if m.filesPos > 0 {
			runes := []rune(m.filesQuery)
			m.filesQuery = string(append(runes[:m.filesPos-1], runes[m.filesPos:]...))
			m.filesPos--
			m.updateFilePickerMatches()
		}
	case tea.KeyDelete:
		runes := []rune(m.filesQuery)
		if m.filesPos < len(runes) {
			m.filesQuery = string(append(runes[:m.filesPos], runes[m.filesPos+1:]...))
			m.updateFilePickerMatches()
		}
	case tea.KeyCtrlA:
		m.filesPos = 0
	case tea.KeyCtrlE:
		m.filesPos = len([]rune(m.filesQuery))
	case tea.KeyCtrlU:
		m.filesQuery = ""
		m.filesPos = 0
		m.updateFilePickerMatches()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.filesVisible = false
			return m, nil
		}
		if m.filesBrowser && len(msg.Runes) == 1 && msg.Runes[0] == '@' {
			if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
				m.addContextFile(m.filesFiltered[m.filesCursor])
			}
			return m, nil
		}
		for _, r := range msg.Runes {
			runes := []rune(m.filesQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.filesPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.filesPos:]...)
			m.filesQuery = string(newRunes)
			m.filesPos++
		}
		m.updateFilePickerMatches()
	}
	return m, nil
}

func (m ChatModel) handleFileViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	_, _, _, _, _, contentHeight, _ := m.filesOverlayLayout()
	maxScroll := max(0, len(strings.Split(m.filesViewText, "\n"))-contentHeight)
	scrollBy := func(delta int) {
		m.filesViewScroll = clamp(m.filesViewScroll+delta, 0, maxScroll)
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.filesViewing = false
	case tea.KeyUp:
		scrollBy(-1)
	case tea.KeyDown:
		scrollBy(1)
	case tea.KeyPgUp:
		scrollBy(-max(1, contentHeight-1))
	case tea.KeyPgDown:
		scrollBy(max(1, contentHeight-1))
	case tea.KeyHome:
		m.filesViewScroll = 0
	case tea.KeyEnd:
		m.filesViewScroll = maxScroll
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'q', 'Q':
				m.filesViewing = false
			case 'j', 'J':
				scrollBy(1)
			case 'k', 'K':
				scrollBy(-1)
			case 'g':
				m.filesViewScroll = 0
			case 'G':
				m.filesViewScroll = maxScroll
			case 'a', 'A', '@':
				m.addContextFile(m.filesViewPath)
			}
		}
	}
	return m, nil
}

func (m ChatModel) resolveExplicitContextPath(query string) string {
	if query == "" {
		return ""
	}
	resolved, err := tools.ResolvePath(m.workDir, query)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(resolved); err != nil {
		return ""
	}
	resolvedWorkDir, err := filepath.EvalSymlinks(m.workDir)
	if err != nil {
		resolvedWorkDir = filepath.Clean(m.workDir)
	}
	rel, err := filepath.Rel(resolvedWorkDir, resolved)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func discoverContextFiles(workDir string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".swp") {
			return nil
		}
		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(files)
	return files, nil
}

func (m *ChatModel) searchNext(delta int) bool {
	if len(m.searchMatches) == 0 {
		return false
	}
	lines, scroll, visible, _ := m.searchTarget()
	if m.searchCurrent < 0 {
		m.searchCurrent = 0
	} else {
		m.searchCurrent = (m.searchCurrent + delta + len(m.searchMatches)) % len(m.searchMatches)
	}
	line := m.searchMatches[m.searchCurrent]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	m.flash = fmt.Sprintf("match %d/%d for %q", m.searchCurrent+1, len(m.searchMatches), m.searchQuery)
	return true
}

func (m ChatModel) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.searchVisible = false
	case tea.KeyEnter:
		m.updateSearchMatches(true)
		m.searchVisible = false
	case tea.KeyLeft:
		if m.searchPos > 0 {
			m.searchPos--
		}
	case tea.KeyRight:
		if m.searchPos < len([]rune(m.searchQuery)) {
			m.searchPos++
		}
	case tea.KeyBackspace:
		if m.searchPos > 0 {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(append(runes[:m.searchPos-1], runes[m.searchPos:]...))
			m.searchPos--
			m.updateSearchMatches(false)
		}
	case tea.KeyDelete:
		runes := []rune(m.searchQuery)
		if m.searchPos < len(runes) {
			m.searchQuery = string(append(runes[:m.searchPos], runes[m.searchPos+1:]...))
			m.updateSearchMatches(false)
		}
	case tea.KeyCtrlA:
		m.searchPos = 0
	case tea.KeyCtrlE:
		m.searchPos = len([]rune(m.searchQuery))
	case tea.KeyCtrlU:
		m.searchQuery = ""
		m.searchPos = 0
		m.updateSearchMatches(false)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.searchQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.searchPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.searchPos:]...)
			m.searchQuery = string(newRunes)
			m.searchPos++
		}
		m.updateSearchMatches(false)
	}
	return m, nil
}

func (m ChatModel) handleStatsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.statsVisible = false
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.statsVisible = false
		}
	}
	return m, nil
}

func (m ChatModel) handleTraceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.traceVisible = false
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.traceVisible = false
		}
	}
	return m, nil
}

func (m *ChatModel) refreshSessionsPicker(resetCursor bool) bool {
	sessions, err := listChatSessions()
	if err != nil {
		m.flash = fmt.Sprintf("sessions failed: %v", err)
		return false
	}
	if len(sessions) == 0 {
		m.sessionsList = nil
		m.sessionsVisible = false
		m.sessionRenaming = false
		m.sessionsCursor = 0
		m.flash = "no saved sessions"
		return false
	}
	currentName := ""
	if !resetCursor && m.sessionsCursor >= 0 && m.sessionsCursor < len(m.sessionsList) {
		currentName = m.sessionsList[m.sessionsCursor].name
	}
	m.sessionsList = sessions
	m.sessionsVisible = true
	m.sessionRenaming = false
	if resetCursor {
		m.sessionsCursor = 0
		return true
	}
	for i, entry := range sessions {
		if entry.name == currentName {
			m.sessionsCursor = i
			return true
		}
	}
	m.sessionsCursor = clamp(m.sessionsCursor, 0, len(sessions)-1)
	return true
}

func (m ChatModel) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sessionRenaming {
		return m.handleSessionRenameKey(msg)
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.sessionsVisible = false
	case tea.KeyUp:
		if m.sessionsCursor > 0 {
			m.sessionsCursor--
		}
	case tea.KeyDown:
		if m.sessionsCursor < len(m.sessionsList)-1 {
			m.sessionsCursor++
		}
	case tea.KeyEnter:
		m.restorePickedSession(m.sessionsCursor)
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					m.restorePickedSession(idx)
				}
				return m, nil
			}
			switch r {
			case 'd', 'D':
				m.deletePickedSession(m.sessionsCursor)
			case 'r', 'R':
				m.beginRenamePickedSession(m.sessionsCursor)
			}
		}
	}
	return m, nil
}

func (m *ChatModel) restorePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if err := m.restoreSession(name); err != nil {
		m.flash = fmt.Sprintf("restore failed: %v", err)
		return
	}
	m.sessionsVisible = false
	m.flash = m.restoredFlash(name)
}

func (m *ChatModel) beginRenamePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot rename last-session"
		return
	}
	m.sessionRenaming = true
	m.sessionRenameBuf = name
	m.sessionRenamePos = len([]rune(name))
}

func (m ChatModel) handleSessionRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.sessionRenaming = false
	case tea.KeyEnter:
		m.commitRenamePickedSession()
	case tea.KeyLeft:
		if m.sessionRenamePos > 0 {
			m.sessionRenamePos--
		}
	case tea.KeyRight:
		if m.sessionRenamePos < len([]rune(m.sessionRenameBuf)) {
			m.sessionRenamePos++
		}
	case tea.KeyBackspace:
		if m.sessionRenamePos > 0 {
			runes := []rune(m.sessionRenameBuf)
			m.sessionRenameBuf = string(append(runes[:m.sessionRenamePos-1], runes[m.sessionRenamePos:]...))
			m.sessionRenamePos--
		}
	case tea.KeyDelete:
		runes := []rune(m.sessionRenameBuf)
		if m.sessionRenamePos < len(runes) {
			m.sessionRenameBuf = string(append(runes[:m.sessionRenamePos], runes[m.sessionRenamePos+1:]...))
		}
	case tea.KeyCtrlA:
		m.sessionRenamePos = 0
	case tea.KeyCtrlE:
		m.sessionRenamePos = len([]rune(m.sessionRenameBuf))
	case tea.KeyCtrlU:
		m.sessionRenameBuf = ""
		m.sessionRenamePos = 0
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.sessionRenameBuf)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.sessionRenamePos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.sessionRenamePos:]...)
			m.sessionRenameBuf = string(newRunes)
			m.sessionRenamePos++
		}
	}
	return m, nil
}

func (m *ChatModel) commitRenamePickedSession() {
	if m.sessionsCursor < 0 || m.sessionsCursor >= len(m.sessionsList) {
		m.sessionRenaming = false
		return
	}
	oldName := m.sessionsList[m.sessionsCursor].name
	newName := sanitizeChatSessionName(m.sessionRenameBuf)
	if newName == "" {
		m.flash = "rename failed: missing session name"
		return
	}
	if oldName == newName {
		m.sessionRenaming = false
		return
	}
	if err := renameChatSession(oldName, newName); err != nil {
		m.flash = fmt.Sprintf("rename failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session renamed: %s -> %s", oldName, newName)
	if !m.refreshSessionsPicker(false) {
		return
	}
	for i, entry := range m.sessionsList {
		if entry.name == newName {
			m.sessionsCursor = i
			break
		}
	}
}

func (m *ChatModel) deletePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot delete last-session"
		return
	}
	if err := deleteChatSession(name); err != nil {
		m.flash = fmt.Sprintf("delete failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session deleted: %s", name)
	if !m.refreshSessionsPicker(false) {
		return
	}
	m.sessionsCursor = clamp(idx, 0, len(m.sessionsList)-1)
}

func (m *ChatModel) openModelPicker() {
	m.ensureModelListLoaded()
	m.modelsQuery = ""
	m.modelsQueryPos = 0
	m.updateModelFilter()
	m.modelsVisible = true
	m.modelsCursor = 0
}

func (m *ChatModel) updateModelFilter() {
	query := strings.TrimSpace(strings.ToLower(m.modelsQuery))
	m.modelsFiltered = m.modelsFiltered[:0]
	for _, name := range m.modelsList {
		label := strings.ToLower(m.modelOptionLabel(name))
		if query == "" || strings.Contains(strings.ToLower(name), query) || strings.Contains(label, query) {
			m.modelsFiltered = append(m.modelsFiltered, name)
		}
	}
	if len(m.modelsFiltered) == 0 {
		m.modelsCursor = 0
		return
	}
	m.modelsCursor = clamp(m.modelsCursor, 0, len(m.modelsFiltered)-1)
}

func (m ChatModel) modelOptionLabel(name string) string {
	if m.config.DescribeModel != nil {
		if label := strings.TrimSpace(m.config.DescribeModel(name)); label != "" {
			return label
		}
	}
	return name
}

func (m ChatModel) uniqueModelOptions(models []string) []string {
	models = uniqueStringsPreserveOrder(models)
	out := make([]string, 0, len(models))
	seen := make(map[string]int, len(models))
	for _, name := range models {
		label := strings.TrimSpace(strings.ToLower(m.modelOptionLabel(name)))
		if label == "" {
			label = strings.TrimSpace(strings.ToLower(name))
		}
		if idx, ok := seen[label]; ok {
			if preferExplicitModelOption(name, out[idx]) {
				out[idx] = name
			}
			continue
		}
		seen[label] = len(out)
		out = append(out, name)
	}
	return out
}

func preferExplicitModelOption(candidate, current string) bool {
	candidateExplicit := strings.Contains(candidate, "/")
	currentExplicit := strings.Contains(current, "/")
	if candidateExplicit != currentExplicit {
		return candidateExplicit
	}
	return false
}

func (m ChatModel) handleModelsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.modelsVisible = false
	case tea.KeyUp:
		if m.modelsCursor > 0 {
			m.modelsCursor--
		}
	case tea.KeyDown:
		if m.modelsCursor < len(m.modelsFiltered)-1 {
			m.modelsCursor++
		}
	case tea.KeyLeft:
		if m.modelsQueryPos > 0 {
			m.modelsQueryPos--
		}
	case tea.KeyRight:
		if m.modelsQueryPos < len([]rune(m.modelsQuery)) {
			m.modelsQueryPos++
		}
	case tea.KeyBackspace:
		if m.modelsQueryPos > 0 {
			runes := []rune(m.modelsQuery)
			m.modelsQuery = string(append(runes[:m.modelsQueryPos-1], runes[m.modelsQueryPos:]...))
			m.modelsQueryPos--
			m.updateModelFilter()
		}
	case tea.KeyDelete:
		runes := []rune(m.modelsQuery)
		if m.modelsQueryPos < len(runes) {
			m.modelsQuery = string(append(runes[:m.modelsQueryPos], runes[m.modelsQueryPos+1:]...))
			m.updateModelFilter()
		}
	case tea.KeyCtrlA:
		m.modelsQueryPos = 0
	case tea.KeyCtrlE:
		m.modelsQueryPos = len([]rune(m.modelsQuery))
	case tea.KeyCtrlU:
		m.modelsQuery = ""
		m.modelsQueryPos = 0
		m.updateModelFilter()
	case tea.KeyEnter:
		return m, m.pickModel(m.modelsCursor)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.modelsQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.modelsQueryPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.modelsQueryPos:]...)
			m.modelsQuery = string(newRunes)
			m.modelsQueryPos++
		}
		m.updateModelFilter()
	}
	return m, nil
}

func (m *ChatModel) ensureModelListLoaded() {
	if len(m.modelsList) > 0 {
		m.modelsList = m.uniqueModelOptions(m.modelsList)
		return
	}
	if len(m.config.AvailableModels) > 0 {
		m.modelsList = m.uniqueModelOptions(m.config.AvailableModels)
		return
	}
	m.refreshModelList()
}

func (m *ChatModel) refreshModelList() {
	var models []string
	if m.config.RefreshModels != nil {
		models = m.config.RefreshModels()
	} else {
		models = m.config.AvailableModels
	}
	m.modelsList = m.uniqueModelOptions(models)
	m.updateModelFilter()
}

func (m *ChatModel) pickModel(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.modelsFiltered) {
		return nil
	}
	picked := m.modelsFiltered[idx]
	if m.config.SwitchModel != nil {
		newModel, err := m.config.SwitchModel(picked)
		if err != nil {
			m.flash = fmt.Sprintf("error: %v", err)
			return nil
		}
		m.model = newModel
		m.resetProviderDiagnostics()
		m.syncStatusData()
		m.flash = fmt.Sprintf("switched to %s", newModel)
	}
	m.modelsVisible = false
	return nil
}

func (m *ChatModel) openProviderPicker() {
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	} else {
		m.providersList = append([]ProviderOption(nil), m.config.Providers...)
	}
	m.providersVisible = true
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerStatus = ""
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthWaiting = false
	m.providerAuthProvider = ""
	m.providerAuthFlow = nil
	m.providersCursor = 0
}

func (m ChatModel) handleProvidersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthWaiting {
		switch msg.Type {
		case tea.KeyCtrlO:
			return m, openProviderAuthURL(m.providerAuthURL)
		case tea.KeyRunes:
			if len(msg.Runes) == 1 && (msg.Runes[0] == 'o' || msg.Runes[0] == 'O') {
				return m, openProviderAuthURL(m.providerAuthURL)
			}
		case tea.KeyEscape:
			if m.providerAuthCancel != nil {
				m.providerAuthCancel()
				m.providerAuthCancel = nil
			}
			m.providerAuthWaiting = false
			m.providerAuthURL = ""
			m.providerAuthCode = ""
			m.providerAuthProvider = ""
			m.providerStatus = "sign-in canceled"
		}
		return m, nil
	}
	if m.providerPromptingKey {
		if m.providerAuthProvider == "claude" && strings.TrimSpace(m.providerAuthURL) != "" {
			switch msg.Type {
			case tea.KeyCtrlO:
				return m, openProviderAuthURL(m.providerAuthURL)
			case tea.KeyRunes:
				if len(msg.Runes) == 1 && (msg.Runes[0] == 'o' || msg.Runes[0] == 'O') && strings.TrimSpace(m.providerKeyInput) == "" {
					return m, openProviderAuthURL(m.providerAuthURL)
				}
			}
		}
		switch msg.Type {
		case tea.KeyEscape:
			m.providerPromptingKey = false
			m.providerPromptLabel = ""
			m.providerPromptMasked = false
			m.providerKeyInput = ""
			m.providerKeyPos = 0
			m.providerAuthProvider = ""
			m.providerAuthFlow = nil
			m.providerStatus = ""
		case tea.KeyEnter:
			return m.saveProviderKey()
		case tea.KeyLeft:
			if m.providerKeyPos > 0 {
				m.providerKeyPos--
			}
		case tea.KeyRight:
			if m.providerKeyPos < len([]rune(m.providerKeyInput)) {
				m.providerKeyPos++
			}
		case tea.KeyBackspace:
			if m.providerKeyPos > 0 {
				runes := []rune(m.providerKeyInput)
				m.providerKeyInput = string(append(runes[:m.providerKeyPos-1], runes[m.providerKeyPos:]...))
				m.providerKeyPos--
			}
		case tea.KeyDelete:
			runes := []rune(m.providerKeyInput)
			if m.providerKeyPos < len(runes) {
				m.providerKeyInput = string(append(runes[:m.providerKeyPos], runes[m.providerKeyPos+1:]...))
			}
		case tea.KeyRunes:
			for _, r := range msg.Runes {
				runes := []rune(m.providerKeyInput)
				newRunes := make([]rune, 0, len(runes)+1)
				newRunes = append(newRunes, runes[:m.providerKeyPos]...)
				newRunes = append(newRunes, r)
				newRunes = append(newRunes, runes[m.providerKeyPos:]...)
				m.providerKeyInput = string(newRunes)
				m.providerKeyPos++
			}
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.providersVisible = false
	case tea.KeyUp:
		if m.providersCursor > 0 {
			m.providersCursor--
		}
	case tea.KeyDown:
		if m.providersCursor < len(m.providersList)-1 {
			m.providersCursor++
		}
	case tea.KeyEnter:
		return m.activateProviderSelection()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'd', 'D':
				return m.deleteProviderCredential()
			}
		}
	}
	return m, nil
}

func (m ChatModel) activateProviderSelection() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	provider := m.providersList[m.providersCursor]
	if providerNeedsInteractiveLogin(provider) {
		return m.startProviderLogin(provider)
	}
	if providerUsesAPIKey(provider.ID) {
		m.providerPromptingKey = true
		m.providerKeyInput = ""
		m.providerKeyPos = 0
		m.providerPromptLabel = "API key"
		m.providerPromptMasked = true
		m.providerStatus = "enter API key"
		return m, nil
	}
	if provider.DefaultModel != "" && m.config.SwitchModel != nil {
		newModel, err := m.config.SwitchModel(provider.DefaultModel)
		if err != nil {
			m.flash = fmt.Sprintf("error: %v", err)
		} else {
			m.model = newModel
			m.resetProviderDiagnostics()
			m.syncStatusData()
			m.flash = fmt.Sprintf("switched to %s", newModel)
			m.providersVisible = false
			return m, nil
		}
	}
	m.providersVisible = false
	return m, nil
}

func providerNeedsInteractiveLogin(provider ProviderOption) bool {
	id := strings.ToLower(strings.TrimSpace(provider.ID))
	if id != "chatgpt" && id != "claude" && id != "copilot" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(provider.Status))
	return strings.Contains(status, "sign in")
}

func (m ChatModel) startProviderLogin(provider ProviderOption) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(provider.ID)) {
	case "chatgpt":
		m.providerStatus = "requesting ChatGPT device code..."
		return m, func() tea.Msg {
			flow, err := startChatGPTDeviceAuth(context.Background())
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  flow.VerificationURL(),
				userCode:   flow.UserCode(),
				flow:       flow,
			}
		}
	case "claude":
		m.providerStatus = "preparing Claude sign-in..."
		return m, func() tea.Msg {
			flow, err := startClaudeAuth()
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  flow.AuthorizationURL,
				flow:       flow,
			}
		}
	case "copilot":
		if strings.TrimSpace(m.config.CopilotClientID) == "" {
			m.providerStatus = "missing Copilot client id"
			return m, nil
		}
		m.providerStatus = "requesting Copilot device code..."
		clientID := m.config.CopilotClientID
		return m, func() tea.Msg {
			dc, err := startCopilotDeviceAuth(context.Background(), clientID)
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  dc.VerificationURI,
				userCode:   dc.UserCode,
				flow:       dc,
			}
		}
	default:
		return m, nil
	}
}

func (m ChatModel) saveProviderKey() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	key := strings.TrimSpace(m.providerKeyInput)
	if key == "" {
		if m.providerAuthProvider == "claude" {
			m.providerStatus = "paste the callback URL or authorization code"
		} else {
			m.providerStatus = "missing API key"
		}
		return m, nil
	}
	if m.providerAuthProvider == "claude" {
		flow, _ := m.providerAuthFlow.(*claudeauth.Flow)
		return m, func() tea.Msg {
			session, err := exchangeClaudeAuth(context.Background(), flow, key)
			if err != nil {
				return providerAuthFailedMsg{providerID: "claude", err: err}
			}
			return providerAuthSucceededMsg{providerID: "claude", claudeSession: &session}
		}
	}
	tokens, err := auth.Load()
	if err != nil {
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	setProviderToken(tokens, m.providersList[m.providersCursor].ID, key)
	if err := auth.Save(tokens); err != nil {
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerStatus = "saved"
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	provider := m.providersList[m.providersCursor]
	if provider.DefaultModel != "" && m.config.SwitchModel != nil {
		if newModel, err := m.config.SwitchModel(provider.DefaultModel); err == nil {
			m.model = newModel
			m.resetProviderDiagnostics()
			m.syncStatusData()
			m.flash = fmt.Sprintf("saved key and switched to %s", newModel)
			return m, nil
		} else {
			m.flash = "saved key"
		}
	} else {
		m.flash = "saved key"
	}
	return m, nil
}

func (m ChatModel) handleProviderAuthStarted(msg providerAuthStartedMsg) (tea.Model, tea.Cmd) {
	switch flow := msg.flow.(type) {
	case *claudeauth.Flow:
		m.providerAuthWaiting = false
		m.providerAuthProvider = msg.providerID
		m.providerAuthURL = msg.verifyURL
		m.providerAuthCode = msg.userCode
		m.providerAuthFlow = flow
		m.providerPromptingKey = true
		m.providerPromptLabel = "Paste callback/code"
		m.providerPromptMasked = false
		m.providerKeyInput = ""
		m.providerKeyPos = 0
		m.providerStatus = "open the browser URL, finish sign-in, then paste the callback URL or authorization code"
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.providerAuthCancel = cancel
	m.providerAuthWaiting = true
	m.providerAuthProvider = msg.providerID
	m.providerAuthURL = msg.verifyURL
	m.providerAuthCode = msg.userCode
	m.providerAuthFlow = msg.flow
	m.providerStatus = fmt.Sprintf("visit %s and enter %s", msg.verifyURL, msg.userCode)

	switch flow := msg.flow.(type) {
	case *chatgptauth.DeviceFlow:
		return m, func() tea.Msg {
			session, err := waitChatGPTDeviceAuth(ctx, flow)
			if err != nil {
				return providerAuthFailedMsg{providerID: msg.providerID, err: err}
			}
			return providerAuthSucceededMsg{providerID: msg.providerID, session: &session}
		}
	case *copilot.DeviceCode:
		clientID := m.config.CopilotClientID
		return m, func() tea.Msg {
			token, err := waitCopilotDeviceAuth(ctx, clientID, flow)
			if err != nil {
				return providerAuthFailedMsg{providerID: msg.providerID, err: err}
			}
			return providerAuthSucceededMsg{providerID: msg.providerID, token: token}
		}
	default:
		return m, nil
	}
}

func (m ChatModel) handleProviderAuthSucceeded(msg providerAuthSucceededMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthCancel != nil {
		m.providerAuthCancel()
		m.providerAuthCancel = nil
	}
	tokens, err := auth.Load()
	if err != nil {
		m.providerAuthWaiting = false
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	switch strings.ToLower(strings.TrimSpace(msg.providerID)) {
	case "chatgpt":
		if msg.session == nil {
			m.providerAuthWaiting = false
			m.providerStatus = "save failed: missing ChatGPT session"
			return m, nil
		}
		tokens = chatgptauth.StoreSession(tokens, *msg.session)
	case "claude":
		if msg.claudeSession == nil {
			m.providerPromptingKey = true
			m.providerStatus = "save failed: missing Claude session"
			return m, nil
		}
		tokens = claudeauth.StoreSession(tokens, *msg.claudeSession)
	case "copilot":
		tokens.CopilotToken = strings.TrimSpace(msg.token)
	}
	if err := auth.Save(tokens); err != nil {
		m.providerAuthWaiting = false
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.providerAuthWaiting = false
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerKeyInput = ""
	m.providerKeyPos = 0
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthProvider = ""
	m.providerAuthFlow = nil
	m.providerStatus = "authenticated"
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	if m.providersCursor >= 0 && m.providersCursor < len(m.providersList) {
		provider := m.providersList[m.providersCursor]
		if provider.DefaultModel != "" && m.config.SwitchModel != nil {
			if newModel, err := m.config.SwitchModel(provider.DefaultModel); err == nil {
				m.model = newModel
				m.resetProviderDiagnostics()
				m.syncStatusData()
				m.flash = fmt.Sprintf("authenticated and switched to %s", newModel)
				return m, nil
			} else {
				m.flash = "authenticated"
			}
		} else {
			m.flash = "authenticated"
		}
	} else {
		m.flash = "authenticated"
	}
	return m, nil
}

func (m ChatModel) handleProviderAuthFailed(msg providerAuthFailedMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthCancel != nil {
		m.providerAuthCancel = nil
	}
	m.providerAuthWaiting = false
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthProvider = ""
	m.providerAuthFlow = nil
	if msg.err != nil {
		m.providerStatus = fmt.Sprintf("sign-in failed: %v", msg.err)
		m.flash = fmt.Sprintf("sign-in failed: %v", msg.err)
	} else {
		m.providerStatus = "sign-in failed"
		m.flash = "sign-in failed"
	}
	return m, nil
}

func (m ChatModel) deleteProviderCredential() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	provider := m.providersList[m.providersCursor]
	tokens, err := auth.Load()
	if err != nil {
		m.providerStatus = fmt.Sprintf("delete failed: %v", err)
		return m, nil
	}
	if !providerHasStoredCredential(tokens, provider.ID) {
		m.providerStatus = "no stored credential"
		return m, nil
	}
	clearProviderToken(tokens, provider.ID)
	if err := auth.SaveExact(tokens); err != nil {
		m.providerStatus = fmt.Sprintf("delete failed: %v", err)
		return m, nil
	}
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	m.providerStatus = "deleted"
	m.flash = fmt.Sprintf("deleted %s key", provider.Label)
	return m, nil
}

func (m ChatModel) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalTabs := len(m.helpTabs())
	pageSize := max(1, m.height-10)
	maxScroll := max(0, len(m.helpLines())-pageSize)

	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.helpVisible = false
	case tea.KeyLeft:
		m.helpTab = (m.helpTab + totalTabs - 1) % totalTabs
		m.helpScroll = 0
	case tea.KeyRight:
		m.helpTab = (m.helpTab + 1) % totalTabs
		m.helpScroll = 0
	case tea.KeyUp:
		m.helpScroll = max(0, m.helpScroll-1)
	case tea.KeyDown:
		m.helpScroll = min(maxScroll, m.helpScroll+1)
	case tea.KeyPgUp:
		m.helpScroll = max(0, m.helpScroll-pageSize)
	case tea.KeyPgDown:
		m.helpScroll = min(maxScroll, m.helpScroll+pageSize)
	case tea.KeyHome:
		m.helpScroll = 0
	case tea.KeyEnd:
		m.helpScroll = maxScroll
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case '?', 'q', 'Q':
				m.helpVisible = false
			case '[', 'h', 'H':
				m.helpTab = (m.helpTab + totalTabs - 1) % totalTabs
				m.helpScroll = 0
			case ']', 'l', 'L':
				m.helpTab = (m.helpTab + 1) % totalTabs
				m.helpScroll = 0
			case 'j', 'J':
				m.helpScroll = min(maxScroll, m.helpScroll+1)
			case 'k', 'K':
				m.helpScroll = max(0, m.helpScroll-1)
			case 'g':
				m.helpScroll = 0
			case 'G':
				m.helpScroll = maxScroll
			}
		}
	}
	return m, nil
}
