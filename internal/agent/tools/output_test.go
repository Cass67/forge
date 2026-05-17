package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"forge/internal/sessionstore"
)

func TestReadOutputReturnsRequestedSlice(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	handle, err := store.Put(context.Background(), "session", []byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}

	tool := NewReadOutput(store)
	got, err := tool.Execute(context.Background(), map[string]any{
		"handle": handle.ID,
		"offset": 4,
		"limit":  6,
	})
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var result readOutputResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("decode result %q: %v", got, err)
	}
	if result.Handle != handle.ID {
		t.Fatalf("handle = %q, want %q", result.Handle, handle.ID)
	}
	if result.Offset != 4 || result.Limit != 6 || result.BytesRead != 6 {
		t.Fatalf("range metadata = offset %d limit %d bytes %d", result.Offset, result.Limit, result.BytesRead)
	}
	if result.Content != "456789" {
		t.Fatalf("content = %q, want requested slice", result.Content)
	}
}

func TestReadOutputDefaultsAndClampsLimit(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	data := strings.Repeat("a", defaultReadOutputLimit+10)
	handle, err := store.Put(context.Background(), "session", []byte(data))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}

	tool := NewReadOutput(store)
	got, err := tool.Execute(context.Background(), map[string]any{"handle": handle.ID})
	if err != nil {
		t.Fatalf("read output with defaults: %v", err)
	}
	var result readOutputResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("decode default result: %v", err)
	}
	if result.Offset != 0 || result.Limit != defaultReadOutputLimit || result.BytesRead != defaultReadOutputLimit {
		t.Fatalf("default range = offset %d limit %d bytes %d", result.Offset, result.Limit, result.BytesRead)
	}

	got, err = tool.Execute(context.Background(), map[string]any{
		"handle": handle.ID,
		"limit":  maxReadOutputLimit + 1,
	})
	if err != nil {
		t.Fatalf("read output with large limit: %v", err)
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("decode clamped result: %v", err)
	}
	if result.Limit != maxReadOutputLimit {
		t.Fatalf("limit = %d, want max clamp %d", result.Limit, maxReadOutputLimit)
	}
}

func TestReadOutputNegativeOffsetReadsFromStart(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	handle, err := store.Put(context.Background(), "session", []byte("abcdef"))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}

	tool := NewReadOutput(store)
	got, err := tool.Execute(context.Background(), map[string]any{
		"handle": handle.ID,
		"offset": -10,
		"limit":  3,
	})
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var result readOutputResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("decode result %q: %v", got, err)
	}
	if result.Offset != 0 || result.BytesRead != 3 || result.Content != "abc" {
		t.Fatalf("negative offset result = offset %d bytes %d content %q, want offset 0 first 3 bytes", result.Offset, result.BytesRead, result.Content)
	}
}

func TestReadOutputRejectsMissingOrInvalidHandle(t *testing.T) {
	tool := NewReadOutput(sessionstore.NewFileOutputStore(t.TempDir()))

	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "handle") {
		t.Fatalf("missing handle error = %v, want clear handle error", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"handle": "missing/" + strings.Repeat("a", 64)}); err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("invalid handle error = %v, want clear output error", err)
	}
}

func TestReadOutputRedactsSecretLookingContent(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	secret := "AKIA" + strings.Repeat("A", 16)
	handle, err := store.Put(context.Background(), "session", []byte("token="+secret))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}

	tool := NewReadOutput(store)
	got, err := tool.Execute(context.Background(), map[string]any{"handle": handle.ID})
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("result contains unredacted secret: %q", got)
	}
	if !strings.Contains(got, "<REDACTED:aws-access-token>") {
		t.Fatalf("result does not include redaction marker: %q", got)
	}
}

func TestReadOutputCannotReconstructSecretFromSmallChunks(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	secret := "AKIA" + strings.Repeat("A", 16)
	stored := "token=" + secret + "\n"
	handle, err := store.Put(context.Background(), "session", []byte(stored))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}

	tool := NewReadOutput(store)
	var reconstructed strings.Builder
	for offset := range len(stored) {
		got, err := tool.Execute(context.Background(), map[string]any{
			"handle": handle.ID,
			"offset": offset,
			"limit":  1,
		})
		if err != nil {
			t.Fatalf("read output offset %d: %v", offset, err)
		}
		var result readOutputResult
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("decode result offset %d: %v", offset, err)
		}
		reconstructed.WriteString(result.Content)
	}

	if strings.Contains(reconstructed.String(), secret) {
		t.Fatalf("small chunk reads reconstructed secret: %q", reconstructed.String())
	}
}
