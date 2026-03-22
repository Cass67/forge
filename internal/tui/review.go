package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ReviewRequested is emitted when the user presses r on the done screen.
type ReviewRequested struct{}

// ReviewFile represents a file in the session output.
type ReviewFile struct {
	Path string // relative to code/
	Size int64
}

// ReviewModel is a file browser for session output.
type ReviewModel struct {
	OutputDir   string
	Files       []ReviewFile
	Cursor      int
	Viewing     bool   // true when viewing a file's content
	FileContent string // content of the currently viewed file
	Scroll      int
	Selected    map[int]bool
	Width       int
	Height      int
	CopyTarget  string
	CopyMsg     string // transient status message
}

func NewReviewModel(outputDir, copyTarget string) ReviewModel {
	m := ReviewModel{
		OutputDir:  outputDir,
		Selected:   make(map[int]bool),
		Width:      80,
		Height:     24,
		CopyTarget: copyTarget,
	}
	m.Files = loadReviewFiles(outputDir)
	return m
}

func loadReviewFiles(outputDir string) []ReviewFile {
	codeDir := filepath.Join(outputDir, "code")
	var files []ReviewFile
	filepath.WalkDir(codeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(codeDir, path)
		info, _ := d.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}
		files = append(files, ReviewFile{Path: rel, Size: size})
		return nil
	})
	return files
}

func (m ReviewModel) Init() tea.Cmd { return nil }

func (m ReviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = sz.Width
		m.Height = sz.Height
		return m, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.Viewing {
		return m.updateViewing(key)
	}
	return m.updateList(key)
}

func (m ReviewModel) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "esc":
		return m, func() tea.Msg { return ReviewDone{} }
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < len(m.Files)-1 {
			m.Cursor++
		}
	case "enter":
		if m.Cursor < len(m.Files) {
			m.viewFile()
		}
	case " ":
		m.Selected[m.Cursor] = !m.Selected[m.Cursor]
		if !m.Selected[m.Cursor] {
			delete(m.Selected, m.Cursor)
		}
	case "a":
		if len(m.Selected) == len(m.Files) {
			m.Selected = make(map[int]bool)
		} else {
			for i := range m.Files {
				m.Selected[i] = true
			}
		}
	case "c":
		m.copySelected()
	}
	return m, nil
}

func (m ReviewModel) updateViewing(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	bodyHeight := m.Height - 3
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	switch key.String() {
	case "q", "esc":
		m.Viewing = false
		m.FileContent = ""
		m.Scroll = 0
	case "up", "k":
		if m.Scroll > 0 {
			m.Scroll--
		}
	case "down", "j":
		m.Scroll++
	case "pgup", "b":
		m.Scroll -= bodyHeight / 2
		if m.Scroll < 0 {
			m.Scroll = 0
		}
	case "pgdown", "f":
		m.Scroll += bodyHeight / 2
	}
	return m, nil
}

func (m *ReviewModel) viewFile() {
	if m.Cursor >= len(m.Files) {
		return
	}
	path := filepath.Join(m.OutputDir, "code", m.Files[m.Cursor].Path)
	data, err := os.ReadFile(path)
	if err != nil {
		m.FileContent = fmt.Sprintf("error reading file: %v", err)
	} else {
		m.FileContent = string(data)
	}
	m.Viewing = true
	m.Scroll = 0
}

func (m *ReviewModel) copySelected() {
	if m.CopyTarget == "" {
		m.CopyMsg = "no copy target set"
		return
	}
	count := 0
	for i := range m.Selected {
		if i >= len(m.Files) {
			continue
		}
		src := filepath.Join(m.OutputDir, "code", m.Files[i].Path)
		dst := filepath.Join(m.CopyTarget, m.Files[i].Path)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if os.WriteFile(dst, data, 0o644) == nil {
			count++
		}
	}
	m.CopyMsg = fmt.Sprintf("copied %d file(s) to %s", count, m.CopyTarget)
}

func (m ReviewModel) View() string {
	if m.Viewing {
		return m.viewFileContent()
	}
	return m.viewList()
}

func (m ReviewModel) viewList() string {
	var sb strings.Builder
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("review files") + "\n\n")

	if len(m.Files) == 0 {
		sb.WriteString(styleDim.Render("no files in session output") + "\n")
	}

	visibleHeight := m.Height - 6
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	start := 0
	if m.Cursor >= visibleHeight {
		start = m.Cursor - visibleHeight + 1
	}
	end := start + visibleHeight
	if end > len(m.Files) {
		end = len(m.Files)
	}

	for i := start; i < end; i++ {
		f := m.Files[i]
		prefix := "  "
		if m.Selected[i] {
			prefix = styleGreen.Render("* ")
		}
		cursor := "  "
		if i == m.Cursor {
			cursor = styleGreen.Render("> ")
		}
		size := formatSize(f.Size)
		sb.WriteString(cursor + prefix + styleBright.Render(f.Path) + "  " + styleDim.Render(size) + "\n")
	}

	if m.CopyMsg != "" {
		sb.WriteString("\n" + styleMid.Render(m.CopyMsg) + "\n")
	}

	sb.WriteString("\n" + styleDim.Render("j/k navigate  enter view  space select  a all  c copy  q back") + "\n")
	return sb.String()
}

func (m ReviewModel) viewFileContent() string {
	var sb strings.Builder
	filename := ""
	if m.Cursor < len(m.Files) {
		filename = m.Files[m.Cursor].Path
	}
	sb.WriteString(styleBold.Render(filename) + "\n\n")

	lines := strings.Split(m.FileContent, "\n")
	bodyHeight := m.Height - 4
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	scroll := m.Scroll
	if scroll > len(lines)-bodyHeight {
		scroll = len(lines) - bodyHeight
	}
	if scroll < 0 {
		scroll = 0
	}

	end := scroll + bodyHeight
	if end > len(lines) {
		end = len(lines)
	}

	for i := scroll; i < end; i++ {
		lineNum := fmt.Sprintf("%4d ", i+1)
		sb.WriteString(styleDim.Render(lineNum) + lines[i] + "\n")
	}

	sb.WriteString("\n" + styleDim.Render("j/k scroll  q back") + "\n")
	return sb.String()
}

func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	return fmt.Sprintf("%.1fK", float64(bytes)/1024)
}

// ReviewDone is emitted when the user exits the review screen.
type ReviewDone struct{}
