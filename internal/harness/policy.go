package harness

func Decide(_ Classification, obs Observation) Decision {
	if obs.Status == ObservationBlocked || obs.Err != nil {
		return Decision{
			FinalState: StateBlocked,
			Reason:     "observation blocked",
		}
	}
	return Decision{
		FinalState: StateComplete,
		Reason:     "observation complete",
	}
}
