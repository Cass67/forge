package chatstate

import "sync"

// State holds shared chat-session state that multiple layers can reference.
type State struct {
	mu              sync.RWMutex
	activatedSkills map[string]bool
}

func New() *State {
	return &State{activatedSkills: make(map[string]bool)}
}

func (s *State) ActivateSkill(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activatedSkills[name] = true
}

func (s *State) SkillActivated(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activatedSkills[name]
}

func (s *State) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activatedSkills = make(map[string]bool)
}
