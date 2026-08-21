package gui

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The preview pane shows the app the agent is working on, and the agent can
// only be told what the user clicked if something in that page is listening.
// Nothing can be injected into another origin from the window, so the page is
// served through a loopback proxy that adds the bridge script on the way past.
//
//go:embed preview_bridge.js
var previewBridge string

// bridgePath is the one path the proxy answers itself. It is namespaced so it
// cannot collide with a route in the app being previewed.
const bridgePath = "/__forge/preview-bridge.js"

// previewBodyLimit bounds the HTML the proxy will buffer to inject into. A
// document past this is streamed through untouched rather than held in memory.
const previewBodyLimit = 8 << 20

// PreviewInfo tells the window where to point its iframe.
type PreviewInfo struct {
	// URL is the proxy the iframe loads; Target is the app behind it.
	URL    string `json:"url"`
	Target string `json:"target"`
}

type previewProxy struct {
	server *http.Server
	target string
	url    string
}

// previewTarget validates what the user typed into the preview's address bar.
// Only loopback targets are accepted: the proxy has no authentication of its
// own, so pointing it at anything else would hand every page it can reach to
// whatever is running in the previewed app.
func previewTarget(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("a preview address is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("cannot read that address: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("preview addresses must be http or https")
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, errors.New("a preview address needs a host")
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("preview only opens apps running on this machine (localhost)")
		}
	}
	return parsed, nil
}

// injectBridge puts the bridge script first in <head>, so it is listening
// before the app's own scripts run and can catch their startup errors.
func injectBridge(html string) string {
	tag := `<script src="` + bridgePath + `" data-forge-preview="1"></script>`
	lower := strings.ToLower(html)
	if at := strings.Index(lower, "<head"); at >= 0 {
		if end := strings.Index(lower[at:], ">"); end >= 0 {
			cut := at + end + 1
			return html[:cut] + tag + html[cut:]
		}
	}
	if at := strings.Index(lower, "<body"); at >= 0 {
		return html[:at] + tag + html[at:]
	}
	return tag + html
}

func newPreviewProxy(target *url.URL) (*previewProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = target.Host
			// Ask upstream for plain text: the HTML has to be readable here
			// to inject into, and a dev server is on the same machine.
			r.Out.Header.Set("Accept-Encoding", "identity")
		},
		ModifyResponse: func(resp *http.Response) error {
			// Framing and script rules are the previewed app's, written for
			// the open web; this pane is the user framing their own dev server
			// on purpose, and the bridge has to be allowed to load.
			resp.Header.Del("X-Frame-Options")
			resp.Header.Del("Content-Security-Policy")
			resp.Header.Del("Content-Security-Policy-Report-Only")
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
				return nil
			}
			if size := resp.ContentLength; size > previewBodyLimit {
				return nil
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, previewBodyLimit))
			_ = resp.Body.Close()
			if err != nil {
				return err
			}
			injected := injectBridge(string(body))
			resp.Body = io.NopCloser(strings.NewReader(injected))
			resp.ContentLength = int64(len(injected))
			resp.Header.Set("Content-Length", strconv.Itoa(len(injected)))
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "forge preview cannot reach "+target.String()+": "+err.Error())
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(bridgePath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, previewBridge)
	})
	mux.Handle("/", proxy)

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return &previewProxy{
		server: server,
		target: target.String(),
		url:    "http://" + listener.Addr().String(),
	}, nil
}

// StartPreview points the preview pane at a locally running app. Calling it
// again re-targets the same proxy, so the iframe's origin — and anything the
// app stored under it — survives an address change.
func (s *Service) StartPreview(target string) (PreviewInfo, error) {
	if _, _, ready := s.snapshot(); !ready {
		return PreviewInfo{}, errNotReady
	}
	parsed, err := previewTarget(target)
	if err != nil {
		return PreviewInfo{}, err
	}
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.preview != nil {
		s.stopPreviewLocked()
	}
	proxy, err := newPreviewProxy(parsed)
	if err != nil {
		return PreviewInfo{}, err
	}
	s.preview = proxy
	return PreviewInfo{URL: proxy.url, Target: proxy.target}, nil
}

func (s *Service) StopPreview() {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	s.stopPreviewLocked()
}

func (s *Service) stopPreviewLocked() {
	if s.preview == nil {
		return
	}
	_ = s.preview.server.Close()
	s.preview = nil
}
