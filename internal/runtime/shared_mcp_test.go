package runtime

import (
	"testing"

	"forge/internal/mcp"
)

// Several chats live in one directory share its MCP servers, and the servers
// only go away when the last of those chats does.
func TestAcquireMCPSharesOneManagerPerWorkspace(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()

	first, isFirst, releaseFirst := acquireMCP(dir, nil)
	if !isFirst {
		t.Fatal("the first chat in a workspace has to be told to connect")
	}
	second, isSecond, releaseSecond := acquireMCP(dir, nil)
	if second != first {
		t.Fatal("a second chat in the same workspace started its own servers")
	}
	if isSecond {
		t.Fatal("a second chat must not reconnect servers that are already up")
	}

	elsewhere, isFirstElsewhere, releaseElsewhere := acquireMCP(other, nil)
	if elsewhere == first {
		t.Fatal("a different workspace shared another one's servers")
	}
	if !isFirstElsewhere {
		t.Fatal("the first chat in a new workspace has to connect")
	}
	releaseElsewhere()

	// One chat ending must leave the other's servers alone: the host survives
	// until its last holder lets go.
	releaseFirst()
	stillThere, isFirstAgain, releaseThird := acquireMCP(dir, nil)
	if stillThere != first {
		t.Fatal("releasing one chat tore down servers another was still using")
	}
	if isFirstAgain {
		t.Fatal("the host was reconnected while it was still in use")
	}
	releaseThird()
	releaseSecond()

	// With everyone gone the host is dropped, so the next chat gets a fresh
	// manager and is told to connect it.
	rebuilt, isFirstRebuilt, releaseRebuilt := acquireMCP(dir, nil)
	defer releaseRebuilt()
	if rebuilt == first {
		t.Fatal("the host outlived its last holder")
	}
	if !isFirstRebuilt {
		t.Fatal("a rebuilt host has to be connected")
	}
}

// Every live chat sees server events, not just whichever one attached last.
// The manager calls the host's broadcast, so that is what is driven here.
func TestAcquireMCPFansOutEventsToEveryChat(t *testing.T) {
	dir := t.TempDir()
	seen := make(chan string, 2)
	_, _, releaseA := acquireMCP(dir, func(ev mcp.Event) { seen <- "a:" + ev.ServerName })
	defer releaseA()
	_, _, releaseB := acquireMCP(dir, func(ev mcp.Event) { seen <- "b:" + ev.ServerName })

	mcpHostsMu.Lock()
	host := mcpHosts[workspaceKey(dir)]
	mcpHostsMu.Unlock()
	if host == nil {
		t.Fatal("no host registered for the workspace")
	}

	host.broadcast(mcp.Event{Kind: mcp.EventRefreshed, ServerName: "srv"})
	got := map[string]bool{<-seen: true, <-seen: true}
	if !got["a:srv"] || !got["b:srv"] {
		t.Fatalf("both chats should have seen the event, got %v", got)
	}

	// A chat that has ended stops hearing about servers.
	releaseB()
	host.broadcast(mcp.Event{Kind: mcp.EventRefreshed, ServerName: "after"})
	if first := <-seen; first != "a:after" {
		t.Fatalf("a released listener was still called: %s", first)
	}
	if len(seen) != 0 {
		t.Fatal("a released listener was still called")
	}
}
