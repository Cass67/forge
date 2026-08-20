package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"forge/internal/sessionstore"
)

const (
	defaultReadOutputLimit = 20000
	maxReadOutputLimit     = 100000
)

type readOutputResult struct {
	Handle    string `json:"handle"`
	Offset    int64  `json:"offset"`
	Limit     int64  `json:"limit"`
	BytesRead int    `json:"bytes_read"`
	Content   string `json:"content"`
}

func NewReadOutput(store sessionstore.OutputStore, policies ...SecretPolicy) Tool {
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "read_output",
		Description: "Read stored tool output by handle. Large tool results are kept out of band and referenced by a handle; this returns their content. Page through anything big with offset and limit.",
		Parameters: []ParameterDef{
			{Name: "handle", Type: "string", Description: "output handle returned with stored tool output", Required: true},
			{Name: "offset", Type: "int", Description: "byte offset to start reading from", Required: false},
			{Name: "limit", Type: "int", Description: "maximum bytes to read", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if store == nil {
				return "", fmt.Errorf("output store unavailable")
			}
			handleID, _ := args["handle"].(string)
			handleID = strings.TrimSpace(handleID)
			if handleID == "" {
				return "", fmt.Errorf("read_output.handle is required")
			}

			handle, err := store.Handle(ctx, handleID)
			if err != nil {
				return "", fmt.Errorf("read output handle %q: %w", handleID, err)
			}

			offset := outputInt64Arg(args["offset"], 0)
			if offset < 0 {
				offset = 0
			}
			limit := outputInt64Arg(args["limit"], defaultReadOutputLimit)
			if limit <= 0 {
				limit = defaultReadOutputLimit
			}
			if limit > maxReadOutputLimit {
				limit = maxReadOutputLimit
			}

			data, err := store.Read(ctx, handle, 0, int64(handle.Bytes))
			if err != nil {
				return "", fmt.Errorf("read output handle %q: %w", handleID, err)
			}
			content, _ := secretPolicy.ApplyCommandOutput(string(data))
			contentBytes := []byte(content)
			if offset >= int64(len(contentBytes)) {
				contentBytes = []byte{}
			} else {
				end := offset + limit
				if end < offset || end > int64(len(contentBytes)) {
					end = int64(len(contentBytes))
				}
				contentBytes = contentBytes[offset:end]
			}
			return encodeReadOutputJSON(readOutputResult{
				Handle:    handle.ID,
				Offset:    offset,
				Limit:     limit,
				BytesRead: len(contentBytes),
				Content:   string(contentBytes),
			})
		},
	}
}

func encodeReadOutputJSON(result readOutputResult) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func outputInt64Arg(value any, fallback int64) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fallback
		}
		return int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
	}
	return fallback
}
