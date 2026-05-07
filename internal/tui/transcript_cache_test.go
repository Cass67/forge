package tui

import "testing"

func TestTranscriptCacheReusesUnchangedMessage(t *testing.T) {
	cache := newTranscriptRenderCache()
	msg := ChatMessage{Kind: MsgAgent, Header: "Agent", Content: "hello"}
	if _, ok := cache.Get(msg, 80, "theme"); ok {
		t.Fatal("unexpected cache hit")
	}
	cache.Put(msg, 80, "theme", "rendered")
	got, ok := cache.Get(msg, 80, "theme")
	if !ok || got != "rendered" {
		t.Fatalf("cache get = %q %v", got, ok)
	}
	if cache.Hits != 1 || cache.Misses != 1 {
		t.Fatalf("stats hits=%d misses=%d", cache.Hits, cache.Misses)
	}
}

func TestTranscriptCacheInvalidatesWidthAndTheme(t *testing.T) {
	cache := newTranscriptRenderCache()
	msg := ChatMessage{Kind: MsgAgent, Header: "Agent", Content: "hello"}
	cache.Put(msg, 80, "theme-a", "rendered")
	if _, ok := cache.Get(msg, 100, "theme-a"); ok {
		t.Fatal("width change should miss")
	}
	if _, ok := cache.Get(msg, 80, "theme-b"); ok {
		t.Fatal("theme change should miss")
	}
}

func TestTranscriptCacheInvalidatesStreamingToken(t *testing.T) {
	cache := newTranscriptRenderCache()
	msg := ChatMessage{Kind: MsgAgent, Header: "Agent", Content: "hello"}
	cache.Put(msg, 80, "theme", "rendered")
	msg.Content = "hello world"
	if _, ok := cache.Get(msg, 80, "theme"); ok {
		t.Fatal("content change should miss")
	}
}

func TestTranscriptCacheCodeblockRespectsLanguageAndWidth(t *testing.T) {
	cache := newTranscriptRenderCache()
	goMsg := ChatMessage{Kind: MsgAgent, Content: "```go\nfmt.Println(1)\n```"}
	tsMsg := ChatMessage{Kind: MsgAgent, Content: "```ts\nconsole.log(1)\n```"}
	cache.Put(goMsg, 80, "theme", "go")
	cache.Put(tsMsg, 80, "theme", "ts")
	if got, ok := cache.Get(goMsg, 80, "theme"); !ok || got != "go" {
		t.Fatalf("go cache = %q %v", got, ok)
	}
	if got, ok := cache.Get(tsMsg, 80, "theme"); !ok || got != "ts" {
		t.Fatalf("ts cache = %q %v", got, ok)
	}
	if _, ok := cache.Get(goMsg, 40, "theme"); ok {
		t.Fatal("codeblock width change should miss")
	}
}

func TestTranscriptVirtualWindowLimitsRenderedMessages(t *testing.T) {
	start, end := transcriptVirtualWindow(1000, 400, 20, 3)
	if start <= 0 || end-start > 26 {
		t.Fatalf("window = %d..%d", start, end)
	}
}

func TestTranscriptVirtualWindowAtBottomIncludesTail(t *testing.T) {
	start, end := transcriptVirtualWindow(1000, 980, 20, 3)
	if end != 1000 || start < 970 {
		t.Fatalf("window = %d..%d", start, end)
	}
}
