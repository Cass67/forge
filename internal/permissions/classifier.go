package permissions

import "context"

type ClassifierDecision string

const (
	ClassifierAllow ClassifierDecision = "allow"
	ClassifierDeny  ClassifierDecision = "deny"
	ClassifierAsk   ClassifierDecision = "ask"
)

type ClassifierRequest struct {
	Action     Action
	Risk       RiskFacts
	Rules      []Rule
	Transcript string
}

type ClassifierResponse struct {
	Decision ClassifierDecision
	Reason   string
}

type Classifier interface {
	Classify(context.Context, ClassifierRequest) (ClassifierResponse, error)
}
