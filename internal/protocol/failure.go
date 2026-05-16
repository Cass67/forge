package protocol

import "strings"

type FailureClass string

const (
	FailureNone                FailureClass = "none"
	FailureModelOutputInvalid  FailureClass = "model_output_invalid"
	FailureToolArgsInvalid     FailureClass = "tool_args_invalid"
	FailurePolicyBlocked       FailureClass = "policy_blocked"
	FailureToolRuntimeFailed   FailureClass = "tool_runtime_failed"
	FailureProviderUnavailable FailureClass = "provider_unavailable"
	FailureUserCancelled       FailureClass = "user_cancelled"
)

type FailureDecision struct {
	Class       FailureClass `json:"class"`
	Recoverable bool         `json:"recoverable"`
	UserVisible bool         `json:"user_visible"`
	Feedback    string       `json:"feedback,omitempty"`
}

func ClassifyToolArgFailure(feedback string) FailureDecision {
	return FailureDecision{Class: FailureToolArgsInvalid, Recoverable: true, UserVisible: false, Feedback: strings.TrimSpace(feedback)}
}

func ClassifyModelOutputFailure(feedback string) FailureDecision {
	return FailureDecision{Class: FailureModelOutputInvalid, Recoverable: true, UserVisible: false, Feedback: strings.TrimSpace(feedback)}
}

func ClassifyPolicyBlocked(feedback string) FailureDecision {
	return FailureDecision{Class: FailurePolicyBlocked, Recoverable: true, UserVisible: true, Feedback: strings.TrimSpace(feedback)}
}

func ClassifyToolExecutionFailure(toolName string, err error) FailureDecision {
	if err == nil {
		return FailureDecision{Class: FailureNone}
	}
	feedback := "error: " + err.Error()
	if toolName == "ask_user_question" {
		return FailureDecision{Class: FailureToolArgsInvalid, Recoverable: true, UserVisible: false, Feedback: feedback}
	}
	return FailureDecision{Class: FailureToolRuntimeFailed, Recoverable: false, UserVisible: true, Feedback: feedback}
}
