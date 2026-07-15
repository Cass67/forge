package tui

import (
	"strings"
	"testing"
	"time"
)

func TestAgentTaskPanelEmptyWhenNoTasks(t *testing.T) {
	m := ChatModel{}
	theme := lookupThemeForTest(t, "default")
	got := m.renderAgentTaskPanel(theme)
	if got != "" {
		t.Fatalf("expected empty panel with no tasks, got: %s", got)
	}
}

func TestAgentTaskPanelShowsActiveTask(t *testing.T) {
	m := ChatModel{
		agentTasks: []chatAgentTaskState{
			{ID: "agent-1", Role: "scout", Status: "running"},
		},
		width:  80,
		height: 28,
	}
	theme := lookupThemeForTest(t, "default")
	got := m.renderAgentTaskPanel(theme)
	if got == "" {
		t.Fatal("expected non-empty panel with active tasks")
	}
	if !strings.Contains(got, "scout") {
		t.Fatalf("expected role 'scout' in panel: %s", got)
	}
}

func TestAgentTaskPanelHeight(t *testing.T) {
	tests := []struct {
		name  string
		tasks []chatAgentTaskState
		want  int
	}{
		{"no tasks", nil, 0},
		{"one task", []chatAgentTaskState{{ID: "a", Role: "r1", Status: "running"}}, 4},
		{"two tasks", []chatAgentTaskState{
			{ID: "a", Role: "r1", Status: "running"},
			{ID: "b", Role: "r2", Status: "running"},
		}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ChatModel{agentTasks: tt.tasks}
			got := m.agentTaskPanelHeight()
			if got != tt.want {
				t.Fatalf("agentTaskPanelHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFormatTaskPanelCardShowsTool(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	now := time.Now()

	task := chatAgentTaskState{
		ID:           "agent-1",
		Role:         "writer",
		Status:       "running",
		LastToolName: "code_search",
	}
	got := formatTaskPanelCard(task, now, 80, theme)
	if got == "" {
		t.Fatal("expected non-empty card")
	}
	if !strings.Contains(got, "writer") {
		t.Fatalf("expected role in output: %s", got)
	}
	if !strings.Contains(got, "code_search") {
		t.Fatalf("expected tool in output: %s", got)
	}
}
