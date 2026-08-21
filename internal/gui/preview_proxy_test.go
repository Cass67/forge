package gui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPreviewTargetAcceptsOnlyLocalApps(t *testing.T) {
	for _, raw := range []string{"localhost:5173", "http://127.0.0.1:3000/app", "https://[::1]:8443"} {
		if _, err := previewTarget(raw); err != nil {
			t.Fatalf("previewTarget(%q) = %v, want a target", raw, err)
		}
	}
	for _, raw := range []string{"", "example.com", "http://10.0.0.5:3000", "file:///etc/passwd", "ftp://localhost"} {
		if _, err := previewTarget(raw); err == nil {
			t.Fatalf("previewTarget(%q) accepted a target it should refuse", raw)
		}
	}
}

func TestInjectBridgeRunsBeforeThePagesOwnScripts(t *testing.T) {
	html := `<!doctype html><html><head><script src="app.js"></script></head><body></body></html>`
	injected := injectBridge(html)
	if strings.Index(injected, bridgePath) > strings.Index(injected, "app.js") {
		t.Fatal("the bridge must be injected ahead of the page's scripts")
	}
	// A fragment with no head still gets the bridge rather than being dropped.
	if !strings.Contains(injectBridge("<body><p>hi</p></body>"), bridgePath) {
		t.Fatal("a document without a head lost the bridge")
	}
}

func TestPreviewProxyServesTheAppWithTheBridgeAdded(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/data.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		// Rules that would stop the pane framing the app or loading the bridge.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		_, _ = io.WriteString(w, "<html><head></head><body>app</body></html>")
	}))
	defer app.Close()

	target, err := url.Parse(app.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := newPreviewProxy(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.server.Close() }()

	page, err := http.Get(proxy.url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = page.Body.Close() }()
	body, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), bridgePath) {
		t.Fatalf("proxied page has no bridge: %s", body)
	}
	if page.Header.Get("X-Frame-Options") != "" || page.Header.Get("Content-Security-Policy") != "" {
		t.Fatal("framing and script rules must be stripped for the preview pane")
	}

	script, err := http.Get(proxy.url + bridgePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Body.Close() }()
	source, err := io.ReadAll(script.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "forge-preview") {
		t.Fatal("the bridge script is not being served")
	}

	// Everything that is not HTML passes through untouched.
	data, err := http.Get(proxy.url + "/data.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Body.Close() }()
	payload, err := io.ReadAll(data.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"ok":true}` {
		t.Fatalf("non-HTML response was rewritten: %s", payload)
	}
}
