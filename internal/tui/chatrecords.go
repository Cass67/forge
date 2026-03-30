package tui

import (
	"fmt"
	"strings"
)

type RecordKind string

const (
	RecordUser      RecordKind = "user"
	RecordAssistant RecordKind = "assistant"
	RecordError     RecordKind = "error"
	RecordSystem    RecordKind = "system"
)

type SegmentKind string

const (
	SegmentText SegmentKind = "text"
	SegmentCode SegmentKind = "code"
)

type Segment struct {
	Kind SegmentKind
	Lang string
	Text string
}

type TranscriptRecord struct {
	ID       string
	TurnID   int
	Kind     RecordKind
	Label    string
	Segments []Segment
	Final    bool
}

func segmentsFromContent(content string) []Segment {
	blocks := parseMessageBlocks(content)
	segments := make([]Segment, 0, len(blocks))
	for _, block := range blocks {
		if block.Body == "" {
			continue
		}
		kind := SegmentText
		if block.Fenced {
			kind = SegmentCode
		}
		segments = append(segments, Segment{
			Kind: kind,
			Lang: block.Lang,
			Text: block.Body,
		})
	}
	return segments
}

func textSegment(content string) []Segment {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	return []Segment{{Kind: SegmentText, Text: content}}
}

func durableRecordKind(msg ChatMessage) (RecordKind, bool) {
	switch msg.Kind {
	case MsgUser:
		return RecordUser, true
	case MsgAgent:
		return RecordAssistant, true
	case MsgForge:
		return RecordSystem, true
	case MsgPlan:
		return RecordSystem, true
	case MsgStatus:
		content := strings.ToLower(strings.TrimSpace(msg.Content))
		if strings.Contains(content, "error") || strings.Contains(content, "failed") || strings.Contains(content, "denied") {
			return RecordError, true
		}
		return RecordSystem, true
	default:
		return "", false
	}
}

func transcriptRecordFromMessage(msg ChatMessage, id string, turnID int) (TranscriptRecord, bool) {
	kind, ok := durableRecordKind(msg)
	if !ok {
		return TranscriptRecord{}, false
	}

	segments := textSegment(msg.Content)
	if kind == RecordAssistant {
		segments = segmentsFromContent(msg.Content)
	}

	return TranscriptRecord{
		ID:       id,
		TurnID:   turnID,
		Kind:     kind,
		Label:    strings.TrimSpace(msg.Header),
		Segments: segments,
		Final:    true,
	}, true
}

func formatTranscriptRecordID(seq int) string {
	return fmt.Sprintf("record-%d", seq)
}
