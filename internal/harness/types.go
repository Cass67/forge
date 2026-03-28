package harness

import (
	"time"

	"forge/internal/skills"
)

type RequestFamily string

const (
	FamilyAnswer    RequestFamily = "answer"
	FamilyInspect   RequestFamily = "inspect"
	FamilyImplement RequestFamily = "implement"
	FamilyDebug     RequestFamily = "debug"
	FamilyVerify    RequestFamily = "verify"
	FamilyResearch  RequestFamily = "research"
	FamilyTransform RequestFamily = "transform"
	FamilyMixed     RequestFamily = "mixed"
)

type RuntimeState string

const (
	StateIntake           RuntimeState = "intake"
	StateClassify         RuntimeState = "classify"
	StatePlanStep         RuntimeState = "plan_step"
	StateAct              RuntimeState = "act"
	StateObserve          RuntimeState = "observe"
	StateDecide           RuntimeState = "decide"
	StateRetry            RuntimeState = "retry"
	StateReplan           RuntimeState = "replan"
	StateRespond          RuntimeState = "respond"
	StateAwaitingFeedback RuntimeState = "awaiting_feedback"
	StateComplete         RuntimeState = "complete"
	StateBlocked          RuntimeState = "blocked"
)

type StepKind string

const (
	StepRespond     StepKind = "respond"
	StepLocal       StepKind = "local"
	StepStrictLocal StepKind = "strict_local"
	StepWorker      StepKind = "worker"
	StepClarify     StepKind = "clarify"
	StepBlocked     StepKind = "blocked"
)

type WorkerKind string

const (
	WorkerNone       WorkerKind = ""
	WorkerReader     WorkerKind = "reader"
	WorkerEditor     WorkerKind = "editor"
	WorkerVerifier   WorkerKind = "verifier"
	WorkerResearcher WorkerKind = "researcher"
)

type ObservationStatus string

const (
	ObservationComplete ObservationStatus = "complete"
	ObservationBlocked  ObservationStatus = "blocked"
)

type ExecutionLane string

const (
	LaneConversational ExecutionLane = "conversational"
	LaneStrictAction   ExecutionLane = "strict_action"
	LaneWorkerSidecar  ExecutionLane = "worker_sidecar"
)

type ThreadKind string

const (
	ThreadDirectAnswer         ThreadKind = "direct_answer"
	ThreadWorkspaceInspect     ThreadKind = "workspace_inspect"
	ThreadWorkspaceChange      ThreadKind = "workspace_change"
	ThreadPreviewCollaboration ThreadKind = "preview_collaboration"
	ThreadVerification         ThreadKind = "verification"
	ThreadExternalResearch     ThreadKind = "external_research"
	ThreadMetaProcess          ThreadKind = "meta_process"
)

type ThreadStatus string

const (
	ThreadActive               ThreadStatus = "active"
	ThreadAwaitingToolProgress ThreadStatus = "awaiting_tool_progress"
	ThreadAwaitingUserFeedback ThreadStatus = "awaiting_user_feedback"
	ThreadAwaitingVerification ThreadStatus = "awaiting_verification"
	ThreadBlocked              ThreadStatus = "blocked"
	ThreadCompleted            ThreadStatus = "completed"
	ThreadCanceled             ThreadStatus = "canceled"
	ThreadSuperseded           ThreadStatus = "superseded"
)

type ThreadPhase string

const (
	ThreadPhaseNone   ThreadPhase = ""
	ThreadPhaseIdeate ThreadPhase = "ideate"
	ThreadPhaseApply  ThreadPhase = "apply"
)

type DeliverableKind string

const (
	DeliverableAnswerOnly                      DeliverableKind = "answer_only"
	DeliverableEvidenceBackedExplanation       DeliverableKind = "evidence_backed_explanation"
	DeliverableWorkspaceChangeWithVerification DeliverableKind = "workspace_change_with_verification"
	DeliverablePreviewAvailableAndRenderable   DeliverableKind = "preview_available_and_renderable"
	DeliverableResearchSummaryWithSources      DeliverableKind = "research_summary_with_sources"
)

type DeliverableStatus string

const (
	DeliverableUnknown     DeliverableStatus = ""
	DeliverableSatisfied   DeliverableStatus = "satisfied"
	DeliverableMissing     DeliverableStatus = "missing"
	DeliverableNotRequired DeliverableStatus = "not_required"
)

type OutcomeKind string

const (
	OutcomeNone             OutcomeKind = ""
	OutcomeComplete         OutcomeKind = "complete"
	OutcomeRetry            OutcomeKind = "retry"
	OutcomeReplan           OutcomeKind = "replan"
	OutcomeAwaitingFeedback OutcomeKind = "awaiting_feedback"
	OutcomeBlocked          OutcomeKind = "blocked"
)

type ProgressMilestoneKind string

const (
	ProgressMilestoneInspect ProgressMilestoneKind = "inspect"
	ProgressMilestoneChange  ProgressMilestoneKind = "change"
	ProgressMilestonePreview ProgressMilestoneKind = "preview"
	ProgressMilestoneVerify  ProgressMilestoneKind = "verify"
	ProgressMilestoneSkill   ProgressMilestoneKind = "skill"
	ProgressMilestoneTool    ProgressMilestoneKind = "tool"
)

type ProgressMilestone struct {
	Kind    ProgressMilestoneKind
	Message string
}

type ActionOutcome struct {
	Lane              ExecutionLane
	Kind              OutcomeKind
	DeliverableKind   DeliverableKind
	DeliverableStatus DeliverableStatus
	Reason            string
}

type TurnIntent string

const (
	TurnIntentNone            TurnIntent = ""
	TurnIntentNewTask         TurnIntent = "new_task"
	TurnIntentContinueThread  TurnIntent = "continue_thread"
	TurnIntentReplayThread    TurnIntent = "replay_thread"
	TurnIntentRepairThread    TurnIntent = "repair_thread"
	TurnIntentCancelThread    TurnIntent = "cancel_thread"
	TurnIntentMetaQuestion    TurnIntent = "meta_question"
	TurnIntentSupersedeThread TurnIntent = "supersede_thread"
)

type UserTurn struct {
	Text       string
	Turn       int
	ReceivedAt time.Time
}

type Classification struct {
	Family                  RequestFamily
	WantsEvaluation         bool
	WantsAction             bool
	WantsInterpretation     bool
	NeedsPolicyGuard        bool
	DetachedPolicyGuard     bool
	NeedsTerseAnswer        bool
	NeedsExternalSources    bool
	CanStayLocal            bool
	PrefersVisibleExecution bool
	IsFollowUp              bool
	TopicKey                string
	TaskText                string
	ResponsePostlude        string
	ThreadIntent            TurnIntent
	Reason                  string
}

type Step struct {
	Lane    ExecutionLane
	Kind    StepKind
	Worker  WorkerKind
	Reason  string
	Summary string
}

type WorkerSkillContext struct {
	Loaded   []skills.Skill
	AutoMode string
}

type WorkerTask struct {
	Kind                              WorkerKind
	Objective                         string
	Context                           string
	TopicKey                          string
	StopCondition                     string
	EvidenceBudget                    int
	RequireRepresentativeFileEvidence bool
	RequireNonReadmeFileEvidence      bool
	SkillContext                      WorkerSkillContext
	PermissionProfile                 []string
	Deadline                          time.Time
}

type ObservedToolCall struct {
	Name string
	Args map[string]any
}

type Observation struct {
	Status        ObservationStatus
	Lane          ExecutionLane
	Response      string
	Summary       string
	TopicKey      string
	Artifact      any
	Runtime       LocalRuntimeSnapshot
	ToolCalls     []ObservedToolCall
	PendingAction PendingAction
	Outcome       ActionOutcome
	Progress      []ProgressMilestone
	SkillUses     []skills.UseRecord
	Err           error
}

type Decision struct {
	FinalState RuntimeState
	Outcome    OutcomeKind
	Reason     string
}

type TurnResult struct {
	Response       string
	Classification Classification
	Step           Step
	Observation    Observation
	Decision       Decision
	Trace          []TraceRecord
}

type ValidatedWorkerResult struct {
	Parsed   any
	Response string
	Summary  string
	Status   ObservationStatus
}

type EvidenceSnapshot struct {
	Turn     int
	TopicKey string
	Summary  string
}

type ArtifactSnapshot struct {
	Turn     int
	Handle   string
	Path     string
	MIMEType string
	Bytes    int
}

func (a ArtifactSnapshot) IsZero() bool {
	return a.Handle == "" && a.Path == "" && a.MIMEType == "" && a.Bytes == 0
}

type PreviewSnapshot struct {
	Turn   int
	Status string
	Handle string
	Root   string
	Path   string
	Port   int
	URL    string
}

func (p PreviewSnapshot) IsZero() bool {
	return p.Status == "" && p.Handle == "" && p.Root == "" && p.Path == "" && p.Port == 0 && p.URL == ""
}

type LocalRuntimeSnapshot struct {
	Artifact ArtifactSnapshot
	Preview  PreviewSnapshot
}

func (s LocalRuntimeSnapshot) IsZero() bool {
	return s.Artifact.IsZero() && s.Preview.IsZero()
}

type ThreadState struct {
	ID                 string
	Kind               ThreadKind
	Status             ThreadStatus
	Phase              ThreadPhase
	Deliverable        DeliverableKind
	SelectedDirection  string
	Family             RequestFamily
	TopicKey           string
	Goal               string
	TaskText           string
	CreatedTurn        int
	UpdatedTurn        int
	SupersedesThreadID string
	Artifact           ArtifactSnapshot
	Preview            PreviewSnapshot
}

func (t ThreadState) IsZero() bool {
	return t.ID == "" && t.Kind == "" && t.Status == "" && t.Phase == "" && t.Deliverable == "" && t.SelectedDirection == "" && t.TopicKey == "" &&
		t.Goal == "" && t.TaskText == "" && t.CreatedTurn == 0 && t.UpdatedTurn == 0 &&
		t.SupersedesThreadID == "" && t.Artifact.IsZero() && t.Preview.IsZero()
}

func (t ThreadState) IsOpen() bool {
	switch t.Status {
	case ThreadActive, ThreadAwaitingToolProgress, ThreadAwaitingUserFeedback, ThreadAwaitingVerification, ThreadBlocked:
		return t.ID != ""
	default:
		return false
	}
}

type ThreadLedger struct {
	Active ThreadState
	Last   ThreadState
	NextID int
}

type PendingAction struct {
	SetAtTurn            int
	Family               RequestFamily
	TopicKey             string
	TaskText             string
	WantsEvaluation      bool
	WantsAction          bool
	WantsInterpretation  bool
	NeedsExternalSources bool
	CanStayLocal         bool
	ResponsePostlude     string
}

func (p PendingAction) IsZero() bool {
	return p.Family == "" && p.TopicKey == "" && p.TaskText == ""
}

type MetaIntent string

const (
	MetaNone           MetaIntent = ""
	MetaProcess        MetaIntent = "process"
	MetaPromptBoundary MetaIntent = "prompt-boundary"
)

type SessionState struct {
	Turn          int
	LastFamily    RequestFamily
	LastTopicKey  string
	LastResponse  string
	LastEvidence  EvidenceSnapshot
	LastArtifact  ArtifactSnapshot
	LastPreview   PreviewSnapshot
	Threads       ThreadLedger
	PendingAction PendingAction
	LastMeta      MetaIntent
	LastMetaTurn  int
}

func (s SessionState) HasRecentEvidence() bool {
	if s.Turn <= 0 || s.LastEvidence.Turn <= 0 {
		return false
	}
	return s.LastEvidence.Turn >= s.Turn-1
}

func (s SessionState) HasRecentMeta() bool {
	if s.Turn <= 0 || s.LastMetaTurn <= 0 {
		return false
	}
	return s.LastMetaTurn >= s.Turn-1
}

func (s SessionState) HasPendingAction() bool {
	if s.Turn <= 0 || s.PendingAction.IsZero() || s.PendingAction.SetAtTurn <= 0 {
		return false
	}
	return s.PendingAction.SetAtTurn >= s.Turn-1
}

func (s SessionState) HasRecentArtifact() bool {
	if s.Turn <= 0 || s.LastArtifact.IsZero() || s.LastArtifact.Turn <= 0 {
		return false
	}
	return s.LastArtifact.Turn >= s.Turn-1
}

func (s SessionState) HasRecentPreview() bool {
	if s.Turn <= 0 || s.LastPreview.IsZero() || s.LastPreview.Turn <= 0 {
		return false
	}
	return s.LastPreview.Turn >= s.Turn-1
}

func (s SessionState) HasActiveThread() bool {
	return s.Threads.Active.IsOpen()
}

func (s SessionState) ActiveThread() ThreadState {
	return s.Threads.Active
}

type TraceRecord struct {
	Timestamp             time.Time
	State                 RuntimeState
	Family                RequestFamily
	Lane                  ExecutionLane
	Step                  StepKind
	Worker                WorkerKind
	Reason                string
	DebugSummary          string
	TopicKey              string
	ThreadID              string
	ThreadKind            ThreadKind
	ThreadStatus          ThreadStatus
	ThreadPhase           ThreadPhase
	ThreadIntent          TurnIntent
	ClaimGuardStatus      string
	WorkspacePolicyAction string
	ToolCallCount         int
	OutcomeKind           OutcomeKind
	DeliverableKind       DeliverableKind
	DeliverableStatus     DeliverableStatus
}
