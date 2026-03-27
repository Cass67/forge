package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type PreviewRuntime struct {
	mu        sync.Mutex
	workDir   string
	approve   ApprovalFunc
	nextID    int
	artifacts map[string]artifactRecord
	server    *http.Server
	listener  net.Listener
	root      string
	port      int
	lastPath  string
}

type artifactRecord struct {
	Handle   string
	Path     string
	AbsPath  string
	MIMEType string
	Bytes    int
}

type artifactWriteResult struct {
	Handle   string `json:"handle"`
	Path     string `json:"path"`
	MIMEType string `json:"mime_type"`
	Bytes    int    `json:"bytes"`
}

func NewPreviewRuntime(workDir string, approve ApprovalFunc) *PreviewRuntime {
	return &PreviewRuntime{
		workDir:   workDir,
		approve:   approve,
		artifacts: make(map[string]artifactRecord),
	}
}

func (r *PreviewRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var closeErr error
	if r.server != nil {
		closeErr = r.server.Close()
	}
	r.server = nil
	if r.listener != nil {
		_ = r.listener.Close()
	}
	r.listener = nil
	r.root = ""
	r.port = 0
	r.lastPath = ""
	return closeErr
}

func (r *PreviewRuntime) recordArtifact(path, absPath, mimeType string, size int) artifactRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	handle := fmt.Sprintf("artifact-%d", r.nextID)
	record := artifactRecord{
		Handle:   handle,
		Path:     filepath.ToSlash(strings.TrimSpace(path)),
		AbsPath:  absPath,
		MIMEType: mimeType,
		Bytes:    size,
	}
	r.artifacts[handle] = record
	return record
}

func (r *PreviewRuntime) artifactByHandle(handle string) (artifactRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.artifacts[strings.TrimSpace(handle)]
	return record, ok
}

func NewArtifactWrite(runtime *PreviewRuntime) Tool {
	var lastDiff string
	return Tool{
		Name:        "artifact_write",
		Description: "Create or overwrite a tracked artifact file for preview or handoff.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "artifact file path", Required: true},
			{Name: "content", Type: "string", Description: "full file content", Required: true},
			{Name: "mime_type", Type: "string", Description: "optional MIME type override", Required: false},
		},
		AutoApprove: false,
		LastDiff: func() string {
			diff := lastDiff
			lastDiff = ""
			return diff
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lastDiff = ""
			if runtime == nil {
				return "", fmt.Errorf("preview runtime unavailable")
			}

			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			mimeType, _ := args["mime_type"].(string)

			resolved, err := ResolvePath(runtime.workDir, path)
			if err != nil {
				return "", err
			}

			var detail string
			existing, readErr := os.ReadFile(resolved)
			if readErr == nil {
				detail = simpleDiff(string(existing), content, path)
			} else {
				preview := content
				lines := strings.Split(preview, "\n")
				if len(lines) > 20 {
					preview = strings.Join(lines[:20], "\n") + "\n... (truncated)"
				}
				detail = fmt.Sprintf("new artifact: %s\n%s", path, preview)
			}
			lastDiff = detail

			if runtime.approve != nil {
				approved, err := runtime.approve(Action{
					Tool:    "artifact_write",
					Summary: fmt.Sprintf("write artifact %s", path),
					Detail:  detail,
				})
				if err != nil {
					return "", err
				}
				if !approved {
					return "artifact_write denied by user", nil
				}
			}

			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return "", fmt.Errorf("create artifact directories: %w", err)
			}
			if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
				return "", fmt.Errorf("write artifact: %w", err)
			}

			mimeType = strings.TrimSpace(mimeType)
			if mimeType == "" {
				mimeType = mime.TypeByExtension(filepath.Ext(resolved))
			}
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			record := runtime.recordArtifact(path, resolved, mimeType, len(content))
			return encodeToolJSON(artifactWriteResult{
				Handle:   record.Handle,
				Path:     record.Path,
				MIMEType: record.MIMEType,
				Bytes:    record.Bytes,
			})
		},
	}
}

func encodeToolJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
