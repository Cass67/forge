package protocol

import (
	"time"

	"forge/internal/llm"
)

const CurrentItemVersion = 1

type ItemKind string

const (
	ItemSessionMeta      ItemKind = "session_meta"
	ItemTurnContext      ItemKind = "turn_context"
	ItemUserMessage      ItemKind = "user_message"
	ItemAssistantMessage ItemKind = "assistant_message"
	ItemToolCall         ItemKind = "tool_call"
	ItemToolResult       ItemKind = "tool_result"
	ItemRetry            ItemKind = "retry"
	ItemFailure          ItemKind = "failure"
	ItemStats            ItemKind = "stats"
	ItemCompaction       ItemKind = "compaction"
	ItemCheckpoint       ItemKind = "checkpoint"
	ItemAgentHandoff     ItemKind = "agent_handoff"
	ItemSkillContext     ItemKind = "skill_context"
	ItemSideEffectIntent ItemKind = "side_effect_intent"
	ItemTurnContract     ItemKind = "turn_contract"
	ItemTurnComplete     ItemKind = "turn_complete"
)

type TurnStatus string

const (
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInterrupted TurnStatus = "interrupted"
	TurnStatusResumable   TurnStatus = "resumable"
)

type Item struct {
	Version          int                   `json:"version"`
	ID               string                `json:"id"`
	ThreadID         string                `json:"thread_id"`
	TurnID           string                `json:"turn_id,omitempty"`
	Seq              int64                 `json:"seq"`
	Kind             ItemKind              `json:"kind"`
	At               time.Time             `json:"at"`
	SessionMeta      *SessionMetaItem      `json:"session_meta,omitempty"`
	TurnContext      *TurnContextItem      `json:"turn_context,omitempty"`
	Message          *MessageItem          `json:"message,omitempty"`
	ToolCall         *ToolCallItem         `json:"tool_call,omitempty"`
	ToolResult       *ToolResultItem       `json:"tool_result,omitempty"`
	Retry            *RetryItem            `json:"retry,omitempty"`
	Failure          *FailureItem          `json:"failure,omitempty"`
	Stats            *StatsItem            `json:"stats,omitempty"`
	Compaction       *CompactionItem       `json:"compaction,omitempty"`
	Checkpoint       *CheckpointItem       `json:"checkpoint,omitempty"`
	AgentHandoff     *AgentHandoffItem     `json:"agent_handoff,omitempty"`
	SkillContext     *SkillContextItem     `json:"skill_context,omitempty"`
	SideEffectIntent *SideEffectIntentItem `json:"side_effect_intent,omitempty"`
	TurnContract     *TurnContractItem     `json:"turn_contract,omitempty"`
	TurnComplete     *TurnCompleteItem     `json:"turn_complete,omitempty"`
}

type SessionMetaItem struct {
	Source string `json:"source,omitempty"`
	CWD    string `json:"cwd,omitempty"`
	Model  string `json:"model,omitempty"`
}

type TurnContextItem struct {
	Input      string `json:"input,omitempty"`
	Mode       string `json:"mode,omitempty"`
	ResponseID string `json:"response_id,omitempty"`
}

type MessageItem struct {
	Role string `json:"role"`
	Text string `json:"text,omitempty"`
}

type ToolCallItem struct {
	ToolName   string         `json:"tool_name"`
	ToolCallID string         `json:"tool_call_id"`
	Args       map[string]any `json:"args,omitempty"`
}

type ToolResultItem struct {
	ToolName      string `json:"tool_name"`
	ToolCallID    string `json:"tool_call_id"`
	Text          string `json:"text,omitempty"`
	Diff          string `json:"diff,omitempty"`
	Handle        string `json:"handle,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	IsError       bool   `json:"is_error,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	OriginalBytes int    `json:"original_bytes,omitempty"`
}

type RetryItem struct {
	Attempt int    `json:"attempt"`
	Reason  string `json:"reason,omitempty"`
}

type FailureItem struct {
	Decision FailureDecision `json:"decision"`
}

type StatsItem struct {
	DurationMillis int64     `json:"duration_ms,omitempty"`
	Usage          llm.Usage `json:"usage"`
}

type CompactionItem struct {
	Summary string `json:"summary,omitempty"`
}

type CheckpointItem struct {
	ID           string   `json:"id"`
	Phase        string   `json:"phase"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type AgentFollowupActionItem struct {
	Kind             string `json:"kind"`
	TargetPath       string `json:"target_path,omitempty"`
	Description      string `json:"description,omitempty"`
	SuggestedCommand string `json:"suggested_command,omitempty"`
	Blocking         bool   `json:"blocking,omitempty"`
}

type AgentWorkspaceIncidentItem struct {
	Kind        string   `json:"kind"`
	Paths       []string `json:"paths,omitempty"`
	Description string   `json:"description,omitempty"`
	Blocking    bool     `json:"blocking,omitempty"`
}

type AgentHandoffItem struct {
	AgentID          string                       `json:"agent_id"`
	RemainingActions []AgentFollowupActionItem    `json:"remaining_actions,omitempty"`
	Incidents        []AgentWorkspaceIncidentItem `json:"incidents,omitempty"`
	Blocking         bool                         `json:"blocking,omitempty"`
}

type SkillContextItem struct {
	Name string `json:"name"`
	Body string `json:"body,omitempty"`
}

type SideEffectGateItem struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

type SideEffectIntentItem struct {
	ID              string               `json:"id"`
	SourceTurn      int                  `json:"source_turn,omitempty"`
	ArtifactPaths   []string             `json:"artifact_paths,omitempty"`
	AllowedPaths    []string             `json:"allowed_paths,omitempty"`
	RequiredActions []string             `json:"required_actions,omitempty"`
	TargetBranch    string               `json:"target_branch,omitempty"`
	Remote          string               `json:"remote,omitempty"`
	WorkspaceRoot   string               `json:"workspace_root,omitempty"`
	Gates           []SideEffectGateItem `json:"gates,omitempty"`
	IncidentMode    bool                 `json:"incident_mode,omitempty"`
	Reason          string               `json:"reason,omitempty"`
}

type ContractActionItem struct {
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

type ArtifactRequirementItem struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type VerificationRequirementItem struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

type EvidenceRecordItem struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary,omitempty"`
}

type ContractGateItem struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

type TurnContractItem struct {
	ID                   string                        `json:"id"`
	SourceTurn           int                           `json:"source_turn,omitempty"`
	Intent               string                        `json:"intent,omitempty"`
	RequiredActions      []ContractActionItem          `json:"required_actions,omitempty"`
	RequiredArtifacts    []ArtifactRequirementItem     `json:"required_artifacts,omitempty"`
	RequiredVerification []VerificationRequirementItem `json:"required_verification,omitempty"`
	Evidence             []EvidenceRecordItem          `json:"evidence,omitempty"`
	Gates                []ContractGateItem            `json:"gates,omitempty"`
	Status               string                        `json:"status,omitempty"`
	Reason               string                        `json:"reason,omitempty"`
}

type TurnCompleteItem struct {
	Status     TurnStatus `json:"status"`
	ResponseID string     `json:"response_id,omitempty"`
}

func (i Item) IsTerminal() bool {
	if i.Kind == ItemTurnComplete {
		return true
	}
	return i.Kind == ItemFailure && (i.Failure == nil || !i.Failure.Decision.Recoverable)
}
