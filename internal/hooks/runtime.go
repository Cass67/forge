package hooks

import (
	"context"
	"strings"
)

type Handler func(context.Context, Event) []Result

type Registry struct {
	handlers map[Point][]registeredHandler
}

type registeredHandler struct {
	name    string
	handler Handler
}

func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[Point][]registeredHandler),
	}
}

func (r *Registry) Register(point Point, name string, handler Handler) {
	if r == nil || handler == nil {
		return
	}
	if r.handlers == nil {
		r.handlers = make(map[Point][]registeredHandler)
	}
	r.handlers[point] = append(r.handlers[point], registeredHandler{
		name:    name,
		handler: handler,
	})
}

func (r *Registry) Dispatch(ctx context.Context, event Event) ExecutionOutput {
	if r == nil {
		return ExecutionOutput{}
	}

	var output ExecutionOutput
	for _, registered := range r.handlers[event.Point] {
		results, failure := callHandler(ctx, event, registered)
		if failure != nil {
			output.Failures = append(output.Failures, *failure)
			continue
		}

		block, overlays, note := normalizeHandlerResults(results)
		if block != nil {
			output.Block = block
			return output
		}

		output.Overlays = append(output.Overlays, overlays...)
		if shouldReplaceNote(output.Note, note) {
			output.Note = note
		}
	}

	return output
}

func callHandler(ctx context.Context, event Event, registered registeredHandler) (results []Result, failure *Failure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = &Failure{
				Point:   event.Point,
				Handler: registered.name,
				Panic:   recovered,
			}
		}
	}()

	return registered.handler(ctx, event), nil
}

func normalizeHandlerResults(results []Result) (*BlockResult, []OverlayResult, *NoteResult) {
	var overlays []OverlayResult
	var note *NoteResult

	for _, result := range results {
		switch value := result.(type) {
		case nil:
			continue
		case OverlayResult:
			overlays = append(overlays, value)
		case *OverlayResult:
			if value != nil {
				overlays = append(overlays, *value)
			}
		case NoteResult:
			candidate := value
			if shouldReplaceNote(note, &candidate) {
				note = &candidate
			}
		case *NoteResult:
			if value != nil && shouldReplaceNote(note, value) {
				candidate := *value
				note = &candidate
			}
		case BlockResult:
			if hasBlockMessage(value) {
				block := value
				return &block, nil, nil
			}
		case *BlockResult:
			if value != nil && hasBlockMessage(*value) {
				block := *value
				return &block, nil, nil
			}
		}
	}

	return nil, overlays, note
}

func shouldReplaceNote(current, candidate *NoteResult) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	return candidate.Priority > current.Priority
}

func hasBlockMessage(block BlockResult) bool {
	return strings.TrimSpace(block.Message) != ""
}
