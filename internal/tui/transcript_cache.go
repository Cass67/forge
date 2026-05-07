package tui

import "fmt"

type transcriptRenderCache struct {
	entries map[string]renderCacheEntry
	Hits    int
	Misses  int
}

type renderCacheEntry struct {
	Width   int
	ThemeID string
	Source  string
	Output  string
}

func newTranscriptRenderCache() *transcriptRenderCache {
	return &transcriptRenderCache{entries: map[string]renderCacheEntry{}}
}

func (c *transcriptRenderCache) Get(msg ChatMessage, width int, themeID string) (string, bool) {
	if c == nil {
		return "", false
	}
	key := transcriptCacheKey(msg, width, themeID)
	entry, ok := c.entries[key]
	if !ok || entry.Width != width || entry.ThemeID != themeID || entry.Source != msg.Content {
		c.Misses++
		return "", false
	}
	c.Hits++
	return entry.Output, true
}

func (c *transcriptRenderCache) Put(msg ChatMessage, width int, themeID, output string) {
	if c == nil {
		return
	}
	c.entries[transcriptCacheKey(msg, width, themeID)] = renderCacheEntry{Width: width, ThemeID: themeID, Source: msg.Content, Output: output}
}

func transcriptCacheKey(msg ChatMessage, width int, themeID string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%d", msg.Kind, msg.Header, msg.Content, width) + "\x00" + themeID
}

func transcriptVirtualWindow(total, offset, height, overscan int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if height < 1 {
		height = 1
	}
	if overscan < 0 {
		overscan = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	start := offset - overscan
	if start < 0 {
		start = 0
	}
	end := offset + height + overscan
	if end > total {
		end = total
	}
	if total-offset <= height+overscan {
		end = total
		start = total - height - overscan
		if start < 0 {
			start = 0
		}
	}
	return start, end
}
