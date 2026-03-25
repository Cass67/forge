package harness

import "context"

type Runner struct {
	session *Session
	trace   *Recorder
	local   LocalExecutor
}

type RunnerConfig struct {
	Session *Session
	Trace   *Recorder
	Local   LocalExecutor
}

func NewRunner(cfg RunnerConfig) *Runner {
	session := cfg.Session
	if session == nil {
		session = NewSession()
	}
	trace := cfg.Trace
	if trace == nil {
		trace = NewRecorder()
	}
	return &Runner{
		session: session,
		trace:   trace,
		local:   cfg.Local,
	}
}

func (r *Runner) Run(ctx context.Context, input string) (TurnResult, error) {
	r.trace.Reset()

	turn := r.session.BeginTurn(input)
	snapshot := r.session.Snapshot()
	r.trace.Add(StateIntake, "", "", WorkerNone, "user turn received", "")

	class := Classify(turn, snapshot)
	r.trace.Add(StateClassify, class.Family, "", WorkerNone, class.Reason, class.TopicKey)

	step := Plan(class, snapshot)
	r.trace.Add(StatePlanStep, class.Family, step.Kind, step.Worker, step.Reason, class.TopicKey)

	r.trace.Add(StateAct, class.Family, step.Kind, step.Worker, step.Summary, class.TopicKey)
	obs, err := r.local.Execute(ctx, turn, class)
	r.trace.Add(StateObserve, class.Family, step.Kind, step.Worker, firstNonEmpty(obs.Summary, "local execution complete"), class.TopicKey)

	decision := Decide(class, obs)
	r.trace.Add(StateDecide, class.Family, step.Kind, step.Worker, decision.Reason, class.TopicKey)

	if decision.FinalState != StateBlocked {
		r.session.Apply(class, obs)
		r.trace.Add(StateRespond, class.Family, StepRespond, WorkerNone, "emit final forge response", class.TopicKey)
		r.trace.Add(StateComplete, class.Family, StepRespond, WorkerNone, "turn complete", class.TopicKey)
	}
	if decision.FinalState == StateBlocked {
		r.trace.Add(StateBlocked, class.Family, step.Kind, step.Worker, decision.Reason, class.TopicKey)
	}

	return TurnResult{
		Response:       obs.Response,
		Classification: class,
		Step:           step,
		Observation:    obs,
		Decision:       decision,
		Trace:          r.trace.Records(),
	}, err
}

func (r *Runner) Trace() []TraceRecord {
	return r.trace.Records()
}
