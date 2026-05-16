package protocol

import (
	"errors"
	"testing"
)

func TestClassifyToolArgFailureIsRecoverableAndHidden(t *testing.T) {
	d := ClassifyToolArgFailure("error: read_file.path is required")
	if d.Class != FailureToolArgsInvalid || !d.Recoverable || d.UserVisible {
		t.Fatalf("decision = %#v", d)
	}
}

func TestClassifyAskUserQuestionExecutionFailureIsRecoverable(t *testing.T) {
	d := ClassifyToolExecutionFailure("ask_user_question", errors.New("at least two options are required"))
	if d.Class != FailureToolArgsInvalid || !d.Recoverable || d.UserVisible {
		t.Fatalf("decision = %#v", d)
	}
}

func TestClassifyWriteFileExecutionFailureIsFatal(t *testing.T) {
	d := ClassifyToolExecutionFailure("write_file", errors.New("disk unavailable"))
	if d.Class != FailureToolRuntimeFailed || d.Recoverable || !d.UserVisible {
		t.Fatalf("decision = %#v", d)
	}
}
