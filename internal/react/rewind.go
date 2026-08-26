package react

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// CheckpointRef identifies a pre-mutation workspace checkpoint taken this session.
type CheckpointRef struct {
	TurnID string
	ID     string
}

// Checkpoints returns the workspace checkpoints recorded this session, ordered
// oldest to newest. Safe to call between turns from the chat loop.
func (r *Runner) Checkpoints() []CheckpointRef {
	if r == nil || len(r.checkpointIDsByTurn) == 0 {
		return nil
	}
	refs := make([]CheckpointRef, 0, len(r.checkpointIDsByTurn))
	for turnID, id := range r.checkpointIDsByTurn {
		refs = append(refs, CheckpointRef{TurnID: turnID, ID: id})
	}
	slices.SortFunc(refs, func(a, b CheckpointRef) int {
		return cmp.Compare(turnNumber(a.TurnID), turnNumber(b.TurnID))
	})
	return refs
}

// RestoreCheckpoint reverts the workspace to the given checkpoint id.
func (r *Runner) RestoreCheckpoint(ctx context.Context, id string) error {
	if r == nil || r.checkpointManager == nil {
		return fmt.Errorf("checkpoints unavailable")
	}
	return r.checkpointManager.Restore(ctx, id)
}

func turnNumber(turnID string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(turnID, "turn-"))
	return n
}
