package tui

import "strings"

type ProgressUpdate struct {
	TurnID     int
	ReplaceKey string
	Message    string
}

type LiveProgressState struct {
	TurnID     int
	ReplaceKey string
	Entries    []string
}

func (s LiveProgressState) Apply(update ProgressUpdate) LiveProgressState {
	message := strings.TrimSpace(update.Message)
	if message == "" {
		return s.Reset()
	}
	next := LiveProgressState{
		TurnID:     update.TurnID,
		ReplaceKey: strings.TrimSpace(update.ReplaceKey),
		Entries:    append([]string(nil), s.Entries...),
	}
	if next.TurnID == 0 {
		next.TurnID = s.TurnID
	}
	if next.ReplaceKey == "" {
		next.ReplaceKey = s.ReplaceKey
	}
	if next.ReplaceKey != "" && next.ReplaceKey == s.ReplaceKey && len(next.Entries) > 0 {
		if next.Entries[len(next.Entries)-1] != message {
			next.Entries[len(next.Entries)-1] = message
		}
		return next
	}
	if len(next.Entries) == 0 || next.Entries[len(next.Entries)-1] != message {
		next.Entries = append(next.Entries, message)
	}
	return next
}

func (s LiveProgressState) LatestMessage() string {
	if len(s.Entries) == 0 {
		return ""
	}
	return strings.TrimSpace(s.Entries[len(s.Entries)-1])
}

func (s LiveProgressState) RenderMessage() string {
	return strings.Join(s.Entries, "\n")
}

func (s LiveProgressState) Finalize() (TranscriptRecord, bool) {
	message := strings.TrimSpace(s.RenderMessage())
	if message == "" {
		return TranscriptRecord{}, false
	}
	return TranscriptRecord{
		TurnID:   s.TurnID,
		Kind:     RecordSystem,
		Label:    "progress",
		Segments: []Segment{{Kind: SegmentText, Text: message}},
		Final:    true,
	}, true
}

func (s LiveProgressState) Reset() LiveProgressState {
	return LiveProgressState{}
}

func (s LiveProgressState) IsZero() bool {
	return s.TurnID == 0 && s.ReplaceKey == "" && len(s.Entries) == 0
}
