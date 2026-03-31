package errors

import (
	"errors"
	"testing"
)

func TestClassifyError_Auth(t *testing.T) {
	for _, msg := range []string{
		"401 Unauthorized",
		"invalid_api_key: abc123",
		"403 Forbidden",
		"authentication failed",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassAuth {
				t.Errorf("expected auth, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("auth errors should not be retryable")
			}
		})
	}
}

func TestClassifyError_Billing(t *testing.T) {
	for _, msg := range []string{
		"insufficient_quota",
		"quota exceeded",
		"billing error",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassBilling {
				t.Errorf("expected billing, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("billing errors should not be retryable")
			}
		})
	}
}

func TestClassifyError_Context(t *testing.T) {
	for _, msg := range []string{
		"context_length_exceeded",
		"maximum context length exceeded",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassContext {
				t.Errorf("expected context, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("context errors should not be retryable")
			}
			if fe.Recovery != "/compact" {
				t.Errorf("expected /compact recovery, got %q", fe.Recovery)
			}
		})
	}
}

func TestClassifyError_Capacity(t *testing.T) {
	for _, msg := range []string{
		"429 Too Many Requests",
		"rate limit exceeded",
		"rate limited",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassCapacity {
				t.Errorf("expected capacity, got %v", fe.Class)
			}
			if !fe.Retryable {
				t.Error("capacity errors should be retryable")
			}
		})
	}
}

func TestClassifyError_Server(t *testing.T) {
	for _, msg := range []string{
		"500 Internal Server Error",
		"502 Bad Gateway",
		"503 Service Unavailable",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassServer {
				t.Errorf("expected server, got %v", fe.Class)
			}
			if !fe.Retryable {
				t.Error("server errors should be retryable")
			}
		})
	}
}

func TestClassifyError_Transient(t *testing.T) {
	for _, msg := range []string{
		"context deadline exceeded",
		"connection reset by peer",
		"timeout",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassRetryable {
				t.Errorf("expected retryable, got %v", fe.Class)
			}
			if !fe.Retryable {
				t.Error("transient errors should be retryable")
			}
		})
	}
}

func TestClassifyError_Client(t *testing.T) {
	for _, msg := range []string{
		"400 Bad Request",
		"404 Not Found",
		"not a valid model id",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassClient {
				t.Errorf("expected client, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("client errors should not be retryable")
			}
		})
	}
}

func TestClassifyError_Nil(t *testing.T) {
	fe := ClassifyError(nil)
	if fe.Class != ErrorClassUnknown {
		t.Errorf("expected unknown, got %v", fe.Class)
	}
}

func TestClassifyError_DataPolicy(t *testing.T) {
	fe := ClassifyError(errors.New("Request blocked by data policy"))
	if fe.Class != ErrorClassClient {
		t.Errorf("expected client, got %v", fe.Class)
	}
	if fe.Type != "data_policy" {
		t.Errorf("expected data_policy type, got %q", fe.Type)
	}
	if fe.Retryable {
		t.Error("data policy errors should not be retryable")
	}
}

func TestClassifyError_DefaultFallback(t *testing.T) {
	fe := ClassifyError(errors.New("something completely weird"))
	if fe.Class != ErrorClassUnknown {
		t.Errorf("expected unknown, got %v", fe.Class)
	}
	if !fe.Retryable {
		t.Error("unknown errors should be retryable by default")
	}
	if fe.UserMessage != "something completely weird" {
		t.Errorf("user message = %q, want original message", fe.UserMessage)
	}
}

func TestForgeError_ErrorAndUnwrap(t *testing.T) {
	raw := errors.New("raw error")
	fe := ForgeError{
		Message: "classified message",
		Raw:     raw,
	}
	if fe.Error() != "classified message" {
		t.Errorf("Error() = %q, want %q", fe.Error(), "classified message")
	}
	if errors.Unwrap(fe) != raw {
		t.Error("Unwrap() did not return raw error")
	}
}

func TestParseRetryAfterDelay(t *testing.T) {
	tests := []struct {
		msg      string
		want     float64
		wantBool bool
	}{
		{"try again in 11.054s", 11.054, true},
		{"Please try again in 5s", 5, true},
		{"retry after 30", 30, true},
		{"rate limit reset in 2.5s", 2.5, true},
		{"no delay here", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got, ok := ParseRetryAfterDelay(tt.msg)
			if ok != tt.wantBool {
				t.Errorf("ok = %v, want %v", ok, tt.wantBool)
			}
			if ok && got != tt.want {
				t.Errorf("delay = %v, want %v", got, tt.want)
			}
		})
	}
}
