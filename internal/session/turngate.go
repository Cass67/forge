package session

import "sync"

type TurnGate struct {
	mu      sync.Mutex
	enabled bool
	waiter  chan struct{}
}

func NewTurnGate() *TurnGate {
	return &TurnGate{}
}

func (g *TurnGate) Enabled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enabled
}

func (g *TurnGate) Toggle() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = !g.enabled
	if !g.enabled && g.waiter != nil {
		close(g.waiter)
		g.waiter = nil
	}
	return g.enabled
}

func (g *TurnGate) Wait() {
	g.mu.Lock()
	if !g.enabled {
		g.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	g.waiter = ch
	g.mu.Unlock()
	<-ch
}

func (g *TurnGate) Advance() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waiter == nil {
		return false
	}
	close(g.waiter)
	g.waiter = nil
	return true
}
