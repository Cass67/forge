package hooks

type Point string

const (
	PointSessionStart      Point = "session_start"
	PointSessionEnd        Point = "session_end"
	PointPermissionRequest Point = "permission_request"
	PointBeforeTool        Point = "before_tool"
	PointAfterTool         Point = "after_tool"
	PointPreCompact        Point = "pre_compact"
	PointPostCompact       Point = "post_compact"
	PointTurnComplete      Point = "turn_complete"
	PointPromptContext     Point = "prompt_context"
	PointChatMessage       Point = "chat_message"
	PointChatParams        Point = "chat_params"
	PointChatHeaders       Point = "chat_headers"
	PointEvent             Point = "event"
)

type Event struct {
	Point     Point
	Snapshot  any
	Transient any
}

type Result interface {
	hookResult()
}

type OverlayResult struct {
	Key        string
	Content    string
	Priority   Priority
	Provenance string
}

func (OverlayResult) hookResult() {}

type NoteResult struct {
	Message    string
	Priority   Priority
	Provenance string
}

func (NoteResult) hookResult() {}

type BlockResult struct {
	Message    string
	Provenance string
}

func (BlockResult) hookResult() {}

type Failure struct {
	Point   Point
	Handler string
	Panic   any
}

type ExecutionOutput struct {
	Overlays []OverlayResult
	Note     *NoteResult
	Block    *BlockResult
	Failures []Failure
}
