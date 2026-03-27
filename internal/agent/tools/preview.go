package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type previewServerResult struct {
	Status string `json:"status"`
	Handle string `json:"handle,omitempty"`
	Root   string `json:"root,omitempty"`
	Path   string `json:"path,omitempty"`
	Port   int    `json:"port,omitempty"`
	URL    string `json:"url,omitempty"`
	Reused bool   `json:"reused"`
}

func NewPreviewServerEnsure(runtime *PreviewRuntime) Tool {
	return Tool{
		Name:        "preview_server_ensure",
		Description: "Ensure a localhost-only preview server is running for a tracked artifact or workspace file.",
		Parameters: []ParameterDef{
			{Name: "handle", Type: "string", Description: "artifact handle returned by artifact_write", Required: false},
			{Name: "path", Type: "string", Description: "workspace file path to preview", Required: false},
			{Name: "port", Type: "int", Description: "optional preferred localhost port", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if runtime == nil {
				return "", fmt.Errorf("preview runtime unavailable")
			}

			targetPath, root, handle, err := runtime.resolvePreviewTarget(args)
			if err != nil {
				return "", err
			}
			port, err := optionalIntArg(args["port"])
			if err != nil {
				return "", err
			}

			actualPort, reused, err := runtime.ensurePreviewServer(root, port)
			if err != nil {
				return "", err
			}

			previewURL := buildPreviewURL(actualPort, targetPath)
			if err := verifyPreviewURL(ctx, previewURL); err != nil {
				return "", err
			}

			runtime.setLastPreviewPath(targetPath, handle)
			return encodeToolJSON(previewServerResult{
				Status: "live",
				Handle: handle,
				Root:   runtime.displayPath(root),
				Path:   runtime.displayPath(filepath.Join(root, filepath.FromSlash(targetPath))),
				Port:   actualPort,
				URL:    previewURL,
				Reused: reused,
			})
		},
	}
}

func NewPreviewServerStatus(runtime *PreviewRuntime) Tool {
	return Tool{
		Name:        "preview_server_status",
		Description: "Show the current localhost preview server status.",
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			_ = args
			if runtime == nil {
				return "", fmt.Errorf("preview runtime unavailable")
			}
			return encodeToolJSON(runtime.PreviewStatus())
		},
	}
}

func (r *PreviewRuntime) resolvePreviewTarget(args map[string]any) (string, string, string, error) {
	handle, _ := args["handle"].(string)
	if strings.TrimSpace(handle) != "" {
		record, ok := r.artifactByHandle(handle)
		if !ok {
			return "", "", "", fmt.Errorf("unknown artifact handle %q", handle)
		}
		targetPath := filepath.Base(record.AbsPath)
		return filepath.ToSlash(targetPath), filepath.Dir(record.AbsPath), record.Handle, nil
	}

	pathArg, _ := args["path"].(string)
	if strings.TrimSpace(pathArg) == "" {
		return "", "", "", fmt.Errorf("preview_server_ensure requires handle or path")
	}
	resolved, err := ResolvePath(r.workDir, pathArg)
	if err != nil {
		return "", "", "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", "", err
	}
	if info.IsDir() {
		return "", resolved, "", nil
	}
	return filepath.ToSlash(filepath.Base(resolved)), filepath.Dir(resolved), "", nil
}

func (r *PreviewRuntime) ensurePreviewServer(root string, requestedPort int) (int, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.server != nil && r.listener != nil && r.root == root && (requestedPort == 0 || requestedPort == r.port) {
		return r.port, true, nil
	}

	if r.server != nil {
		_ = r.server.Close()
	}
	if r.listener != nil {
		_ = r.listener.Close()
	}

	addr := "127.0.0.1:0"
	if requestedPort > 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", requestedPort)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, false, err
	}

	server := &http.Server{
		Handler: http.FileServer(http.Dir(root)),
	}
	go func() {
		_ = server.Serve(listener)
	}()

	r.server = server
	r.listener = listener
	r.root = root
	r.port = listener.Addr().(*net.TCPAddr).Port
	return r.port, false, nil
}

func (r *PreviewRuntime) setLastPreviewPath(targetPath, handle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastPath = targetPath
	r.lastPreviewHandle = strings.TrimSpace(handle)
}

func (r *PreviewRuntime) PreviewStatus() previewServerResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.server == nil || r.listener == nil || r.port == 0 {
		return previewServerResult{Status: "stopped"}
	}

	return previewServerResult{
		Status: "live",
		Handle: r.lastPreviewHandle,
		Root:   r.displayPath(r.root),
		Path:   r.displayPath(filepath.Join(r.root, filepath.FromSlash(r.lastPath))),
		Port:   r.port,
		URL:    buildPreviewURL(r.port, r.lastPath),
		Reused: false,
	}
}

func (r *PreviewRuntime) displayPath(abs string) string {
	abs = strings.TrimSpace(abs)
	if abs == "" {
		return ""
	}
	rel, err := filepath.Rel(r.workDir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

func buildPreviewURL(port int, targetPath string) string {
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", port),
		Path:   path.Clean("/" + filepath.ToSlash(strings.TrimSpace(targetPath))),
	}
	return u.String()
}

func verifyPreviewURL(ctx context.Context, target string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("preview returned %s", resp.Status)
	}
	return nil
}

func optionalIntArg(raw any) (int, error) {
	switch value := raw.(type) {
	case nil:
		return 0, nil
	case int:
		return value, nil
	case int32:
		return int(value), nil
	case int64:
		return int(value), nil
	case float64:
		return int(value), nil
	default:
		return 0, fmt.Errorf("expected numeric port, got %T", raw)
	}
}
