package gui

import (
	"errors"
	"testing"

	"forge/internal/tui"
)

func serviceWithThreads(ids []string, active string, refuse map[string]bool) *Service {
	live := append([]string(nil), ids...)
	s, c := New(func(string, any) {})
	c.Attach("s1", tui.ChatLiveConfig{
		CurrentThreadID: func() string { return active },
		ListThreads: func() []tui.ThreadSummary {
			// Only ever hands back 2 at a time, the way the real store caps
			// its listing, so a single pass cannot see everything.
			out := []tui.ThreadSummary{}
			for _, id := range live {
				if len(out) == 2 {
					break
				}
				out = append(out, tui.ThreadSummary{ThreadID: id})
			}
			return out
		},
		DeleteThread: func(id string) error {
			if refuse[id] {
				return errors.New("nope")
			}
			for i, have := range live {
				if have == id {
					live = append(live[:i], live[i+1:]...)
					return nil
				}
			}
			return errors.New("not found")
		},
	}, make(chan string, 1), nil)
	return s
}

// The sidebar could only tick threads whose workspace still had a section, so
// clearing works off the stored list and keeps going past the listing cap.
func TestClearThreadsRemovesEveryThreadPastTheListingCap(t *testing.T) {
	s := serviceWithThreads([]string{"a", "b", "c", "d", "e"}, "", nil)

	res, err := s.ClearThreads()
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 5 || res.Failed != 0 {
		t.Fatalf("removed=%d failed=%d, want 5/0", res.Removed, res.Failed)
	}
	if len(res.Threads) != 0 {
		t.Fatalf("threads left: %+v", res.Threads)
	}
}

// The thread being written to is not deletable, and must not be reported as a
// failure either.
func TestClearThreadsKeepsTheActiveThread(t *testing.T) {
	s := serviceWithThreads([]string{"a", "b", "c"}, "b", nil)

	res, err := s.ClearThreads()
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Failed != 0 {
		t.Fatalf("removed=%d failed=%d, want 2/0", res.Removed, res.Failed)
	}
	if len(res.Threads) != 1 || res.Threads[0].ThreadID != "b" {
		t.Fatalf("active thread not kept: %+v", res.Threads)
	}
}

// A thread that refuses to go is counted once, however many passes run, and
// does not stop the others from going.
func TestClearThreadsCountsAStuckThreadOnce(t *testing.T) {
	s := serviceWithThreads([]string{"a", "b", "c"}, "", map[string]bool{"b": true})

	res, err := s.ClearThreads()
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Failed != 1 {
		t.Fatalf("removed=%d failed=%d, want 2/1", res.Removed, res.Failed)
	}
}
