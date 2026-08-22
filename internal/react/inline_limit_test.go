package react

import (
	"context"
	"strings"
	"testing"

	"forge/internal/sessionstore"
)

// A tool result is inlined or offloaded based on the active model's context
// window: a fixed byte count made ordinary source files unreadable in one go
// on large-window models, which then paged them back call by call.
func TestInlineToolResultLimitFollowsContextWindow(t *testing.T) {
	window := func(n int) func() int { return func() int { return n } }

	if got := inlineToolResultLimit(0, nil); got != defaultOutputStoreThresholdBytes {
		t.Fatalf("no window: limit = %d, want the %d floor", got, defaultOutputStoreThresholdBytes)
	}
	if got := inlineToolResultLimit(0, window(32_000)); got != defaultOutputStoreThresholdBytes {
		t.Fatalf("small window: limit = %d, want the floor", got)
	}

	// 262K-token window: a 68KB source file must inline.
	got := inlineToolResultLimit(0, window(262_144))
	if got <= 68_750 {
		t.Fatalf("262K window: limit = %d, want > 68750 so a large source file inlines", got)
	}
	if got > maxInlineToolResultBytes {
		t.Fatalf("262K window: limit = %d, want <= cap %d", got, maxInlineToolResultBytes)
	}
	if got := inlineToolResultLimit(0, window(4_000_000)); got != maxInlineToolResultBytes {
		t.Fatalf("huge window: limit = %d, want cap %d", got, maxInlineToolResultBytes)
	}
	if got := inlineToolResultLimit(4096, window(262_144)); got != 4096 {
		t.Fatalf("explicit config = %d, want it to win", got)
	}
}

// Whatever may be inlined must stay under the compaction trigger, or the next
// decision micro-compacts it away and inlining bought nothing.
func TestLargeToolResultStaysAboveInlineLimit(t *testing.T) {
	for _, tokens := range []int{0, 32_000, 262_144, 1_000_000, 4_000_000} {
		win := func() int { return tokens }
		inline := inlineToolResultLimit(0, win)
		large := largeToolResultBytes(0, win)
		if large <= inline {
			t.Fatalf("window %d: large trigger %d must exceed inline limit %d", tokens, large, inline)
		}
		if large < defaultLargeToolResultBytes {
			t.Fatalf("window %d: large trigger %d below floor", tokens, large)
		}
	}
}

type countingOutputStore struct {
	puts int
}

func (s *countingOutputStore) Put(ctx context.Context, threadID string, data []byte) (sessionstore.OutputHandle, error) {
	s.puts++
	return sessionstore.OutputHandle{ID: "h1", Bytes: len(data)}, nil
}

func (s *countingOutputStore) Handle(ctx context.Context, id string) (sessionstore.OutputHandle, error) {
	return sessionstore.OutputHandle{ID: id}, nil
}

func (s *countingOutputStore) Read(ctx context.Context, handle sessionstore.OutputHandle, offset, limit int64) ([]byte, error) {
	return nil, nil
}

// The decision that actually matters: a line-numbered 1842-line source file —
// the shape that made the agent page one file 43 times — must reach the model
// whole on a gateway-class context window, and a genuinely oversized result
// must still be offloaded.
func TestLargeSourceFileInlinesOnWideContextWindow(t *testing.T) {
	const openaiGoDecodedBytes = 81_644 // 68,750 of source + a line number per line

	store := &countingOutputStore{}
	r := &Runner{
		outputStore:         store,
		contextWindowTokens: func() int { return 202_752 }, // smallest opencode-go window
	}

	item, err := r.toolResultItem(context.Background(), 1, "read_file", "call_1",
		strings.Repeat("x", openaiGoDecodedBytes))
	if err != nil {
		t.Fatal(err)
	}
	if item.Handle != "" || store.puts != 0 {
		t.Fatalf("file was offloaded (handle=%q, puts=%d); it must inline", item.Handle, store.puts)
	}
	if len(item.Text) != openaiGoDecodedBytes {
		t.Fatalf("inlined text = %d bytes, want the whole %d", len(item.Text), openaiGoDecodedBytes)
	}

	// Well past the budget: offloading is still the right call.
	huge := strings.Repeat("x", 300*1024)
	item, err = r.toolResultItem(context.Background(), 1, "read_file", "call_2", huge)
	if err != nil {
		t.Fatal(err)
	}
	if item.Handle == "" || store.puts != 1 {
		t.Fatalf("300KB result was inlined; it must be offloaded (handle=%q, puts=%d)", item.Handle, store.puts)
	}
}
