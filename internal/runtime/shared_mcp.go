package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"forge/internal/config"
	"forge/internal/mcp"
)

// MCP servers are processes and network connections owned by a workspace, not
// by a conversation. The GUI keeps several chats live in one directory at
// once, so a manager per chat meant one copy of every server per open
// conversation. They are shared per directory instead: the first chat in a
// directory starts them, the last one out closes them.
type mcpHost struct {
	manager *mcp.Manager
	mu      sync.Mutex
	refs    int
	nextID  int
	// Each live chat listens for its own tool registrations and status
	// messages, so events fan out rather than landing on whichever chat
	// attached last.
	listeners map[int]func(mcp.Event)
}

var (
	mcpHostsMu sync.Mutex
	mcpHosts   = map[string]*mcpHost{}
)

func (h *mcpHost) broadcast(ev mcp.Event) {
	h.mu.Lock()
	listeners := make([]func(mcp.Event), 0, len(h.listeners))
	for _, fn := range h.listeners {
		listeners = append(listeners, fn)
	}
	h.mu.Unlock()
	for _, fn := range listeners {
		fn(ev)
	}
}

// acquireMCP hands back the directory's MCP manager and a release. onEvent is
// called for every server event while the caller holds it. The returned
// started flag reports whether this caller is the one that has to connect the
// servers; later callers join a manager whose tools are already there.
func acquireMCP(dir string, onEvent func(mcp.Event)) (manager *mcp.Manager, first bool, release func()) {
	key := workspaceKey(dir)
	mcpHostsMu.Lock()
	host, ok := mcpHosts[key]
	if !ok {
		host = &mcpHost{manager: newChatMCPManager(), listeners: map[int]func(mcp.Event){}}
		host.manager.SetEventHandler(host.broadcast)
		mcpHosts[key] = host
	}
	mcpHostsMu.Unlock()

	host.mu.Lock()
	host.refs++
	first = host.refs == 1
	id := host.nextID
	host.nextID++
	if onEvent != nil {
		host.listeners[id] = onEvent
	}
	host.mu.Unlock()

	release = sync.OnceFunc(func() {
		host.mu.Lock()
		delete(host.listeners, id)
		host.refs--
		last := host.refs == 0
		host.mu.Unlock()
		if !last {
			return
		}
		mcpHostsMu.Lock()
		// A chat started in the meantime has taken the host over, so the
		// servers it is using must not be closed underneath it.
		if mcpHosts[key] == host {
			host.mu.Lock()
			stillIdle := host.refs == 0
			host.mu.Unlock()
			if stillIdle {
				delete(mcpHosts, key)
				mcpHostsMu.Unlock()
				_ = host.manager.Close()
				return
			}
		}
		mcpHostsMu.Unlock()
	})
	return host.manager, first, release
}

// connectMCP brings a freshly started host's servers up. Connecting is a
// network round trip per server, so it runs in the background and tools
// register themselves as servers land.
func connectMCP(manager *mcp.Manager, cfg *config.Config, done func()) {
	go func() {
		_ = manager.Refresh(context.Background(), cfg)
		if done != nil {
			done()
		}
	}()
}

func workspaceKey(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return ""
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return abs
	}
	return filepath.Clean(trimmed)
}
