package harness

import "time"

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
	StateIntake   RuntimeState = "intake"
	StateClassify RuntimeState = "classify"
	StatePlanStep RuntimeState = "plan_step"
	StateAct      RuntimeState = "act"
	StateObserve  RuntimeState = "observe"
	StateDecide   RuntimeState = "decide"
	StateRespond  RuntimeState = "respond"
	StateComplete RuntimeState = "complete"
	StateBlocked  RuntimeState = "blocked"
)

type StepKind string

const (
	StepRespond StepKind = "respond"
	StepLocal   StepKind = "local"
	StepWorker  StepKind = "worker"
	StepClarify StepKind = "clarify"
	StepBlocked StepKind = "blocked"
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

type UserTurn struct {
	Text       string
	Turn       int
	ReceivedAt time.Time
}

type Classification struct {
	Family               RequestFamily
	WantsEvaluation      bool
	WantsAction          bool
	WantsInterpretation  bool
	NeedsExternalSources bool
	CanStayLocal         bool
	IsFollowUp           bool
	TopicKey             string
	Reason               string
}

type Step struct {
	Kind    StepKind
	Worker  WorkerKind
	Reason  string
	Summary string
}

type WorkerTask struct {
	Kind           WorkerKind
	Objective      string
	Context        string
	TopicKey       string
	StopCondition  string
	EvidenceBudget int
}

type Observation struct {
	Status   ObservationStatus
	Response string
	Summary  string
	TopicKey string
	Artifact any
	Err      error
}

type Decision struct {
	FinalState RuntimeState
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

type SessionState struct {
	Turn         int
	LastFamily   RequestFamily
	LastTopicKey string
	LastResponse string
	LastEvidence EvidenceSnapshot
}

func (s SessionState) HasRecentEvidence() bool {
	if s.Turn <= 0 || s.LastEvidence.Turn <= 0 {
		return false
	}
	return s.LastEvidence.Turn >= s.Turn-1
}

type TraceRecord struct {
	Timestamp    time.Time
	State        RuntimeState
	Family       RequestFamily
	Step         StepKind
	Worker       WorkerKind
	Reason       string
	DebugSummary string
	TopicKey     string
}
