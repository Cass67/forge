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
	Message    string
}

func (s LiveProgressState) Apply(update ProgressUpdate) LiveProgressState {
	message := strings.TrimSpace(update.Message)
	if message == "" {
		return s.Reset()
	}
	return LiveProgressState{
		TurnID:     update.TurnID,
		ReplaceKey: strings.TrimSpace(update.ReplaceKey),
		Message:    message,
	}
}

func (s LiveProgressState) Finalize() (TranscriptRecord, bool) {
	if strings.TrimSpace(s.Message) == "" {
		return TranscriptRecord{}, false
	}
	return TranscriptRecord{
		TurnID:   s.TurnID,
		Kind:     RecordSystem,
		Label:    "progress",
		Segments: []Segment{{Kind: SegmentText, Text: s.Message}},
		Final:    true,
	}, true
}

func (s LiveProgressState) Reset() LiveProgressState {
	return LiveProgressState{}
}
