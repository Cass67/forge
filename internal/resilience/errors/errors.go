package errors

import (
	stderrors "errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrTruncatedStream marks a streamed response that ended before the provider
// sent a terminal finish_reason: the transport cut the response short, so the
// turn must be retried rather than read as the model's answer. It lives here
// so both the drivers that raise it and the classifier can share one identity.
var ErrTruncatedStream = stderrors.New("stream ended without finish_reason")

// ErrorClass categorises API errors for routing decisions.
type ErrorClass int

const (
	ErrorClassUnknown ErrorClass = iota
	ErrorClassRetryable
	ErrorClassCapacity
	ErrorClassAuth
	ErrorClassContext
	ErrorClassBilling
	ErrorClassClient
	ErrorClassServer
)

func (c ErrorClass) String() string {
	switch c {
	case ErrorClassRetryable:
		return "retryable"
	case ErrorClassCapacity:
		return "capacity"
	case ErrorClassAuth:
		return "auth"
	case ErrorClassContext:
		return "context"
	case ErrorClassBilling:
		return "billing"
	case ErrorClassClient:
		return "client"
	case ErrorClassServer:
		return "server"
	default:
		return "unknown"
	}
}

// ForgeError is a classified API error with user-facing messaging.
type ForgeError struct {
	Class       ErrorClass
	Type        string
	Message     string
	UserMessage string
	Retryable   bool
	Recovery    string
	Raw         error
}

func (e ForgeError) Error() string {
	return e.Message
}

func (e ForgeError) Unwrap() error { return e.Raw }

// ClassifyError inspects an error string and returns a ForgeError.
func ClassifyError(err error) ForgeError {
	if err == nil {
		return ForgeError{Class: ErrorClassUnknown}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	// Truncated streams — always retry. Matched by identity so a provider
	// echoing this wording in a message cannot be mistaken for one.
	if stderrors.Is(err, ErrTruncatedStream) {
		return ForgeError{
			Class:       ErrorClassRetryable,
			Type:        "truncated_stream",
			Message:     msg,
			UserMessage: "Response was cut off mid-stream. Retrying…",
			Retryable:   true,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Auth errors — never retry
	for _, pattern := range authPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassAuth,
				Type:        "auth_error",
				Message:     msg,
				UserMessage: "Authentication failed. Check your API key or run forge login.",
				Retryable:   false,
				Recovery:    "/login",
				Raw:         err,
			}
		}
	}

	// Billing/quota errors — never retry
	for _, pattern := range billingPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassBilling,
				Type:        "billing_error",
				Message:     msg,
				UserMessage: "Billing or quota error. Check your account limits.",
				Retryable:   false,
				Recovery:    "",
				Raw:         err,
			}
		}
	}

	// Context errors — never retry, suggest compaction
	for _, pattern := range contextPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassContext,
				Type:        "context_exceeded",
				Message:     msg,
				UserMessage: "Context window exceeded. The session will be compacted automatically.",
				Retryable:   false,
				Recovery:    "/compact",
				Raw:         err,
			}
		}
	}

	// Capacity / rate limit errors
	if fe := tryClassifyCapacity(lower, msg, err); fe != nil {
		return *fe
	}

	// Client errors (400, 404, 410) — never retry
	for _, pattern := range clientPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassClient,
				Type:        "client_error",
				Message:     msg,
				UserMessage: "Bad request. The model or request parameters may be invalid.",
				Retryable:   false,
				Recovery:    "/model",
				Raw:         err,
			}
		}
	}

	// Server errors (5xx) — retry
	if strings.Contains(lower, "500") || strings.Contains(lower, "502") ||
		strings.Contains(lower, "503") || strings.Contains(lower, "504") ||
		strings.Contains(lower, "internal server error") ||
		strings.Contains(lower, "bad gateway") ||
		strings.Contains(lower, "service unavailable") {
		return ForgeError{
			Class:       ErrorClassServer,
			Type:        "server_error",
			Message:     msg,
			UserMessage: "Provider server error. Retrying…",
			Retryable:   true,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Timeout / connection errors — retry
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "connection reset") || strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "econnreset") || strings.Contains(lower, "epipe") {
		return ForgeError{
			Class:       ErrorClassRetryable,
			Type:        "transient_error",
			Message:     msg,
			UserMessage: "Connection issue. Retrying…",
			Retryable:   true,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Data policy errors — never retry
	if strings.Contains(lower, "data policy") {
		return ForgeError{
			Class:       ErrorClassClient,
			Type:        "data_policy",
			Message:     msg,
			UserMessage: "Request blocked by data policy.",
			Retryable:   false,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Default: retryable unknown
	return ForgeError{
		Class:       ErrorClassUnknown,
		Type:        "unknown",
		Message:     msg,
		UserMessage: msg,
		Retryable:   true,
		Recovery:    "",
		Raw:         err,
	}
}

// tryClassifyCapacity checks for rate limit / capacity errors.
func tryClassifyCapacity(lower, msg string, err error) *ForgeError {
	if strings.Contains(lower, "429") || strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit exceeded") || strings.Contains(lower, "rate limited") ||
		strings.Contains(lower, "server overloaded") || strings.Contains(lower, "capacity overloaded") {
		return &ForgeError{
			Class:       ErrorClassCapacity,
			Type:        "rate_limited",
			Message:     msg,
			UserMessage: "Rate limited. Waiting before retry…",
			Retryable:   true,
			Recovery:    "",
			Raw:         err,
		}
	}
	return nil
}

// ParseRetryAfterDelay extracts a retry delay in seconds from an error message.
// Handles formats like "try again in 11.054s", "retry after 30", "rate limit reset in 5s".
func ParseRetryAfterDelay(msg string) (float64, bool) {
	for _, re := range retryDelayRegexes {
		if m := re.FindStringSubmatch(strings.ToLower(msg)); m != nil {
			if len(m) >= 2 {
				var delay float64
				if _, err := fmt.Sscanf(m[1], "%f", &delay); err == nil && delay > 0 {
					return delay, true
				}
			}
		}
	}
	return 0, false
}

var retryDelayRegexes = []*regexp.Regexp{
	regexp.MustCompile(`try again in ([\d.]+)\s*s`),
	regexp.MustCompile(`retry after ([\d.]+)`),
	regexp.MustCompile(`rate limit reset in ([\d.]+)\s*s`),
}

var authPatterns = []string{
	"401 unauthorized", "403 forbidden", "invalid_api_key", "authentication",
	"incorrect api key", "unauthorized",
}

var billingPatterns = []string{
	"insufficient_quota", "quota exceeded", "billing",
	"usage limit", "insufficient credit",
}

var contextPatterns = []string{
	"context_length_exceeded", "maximum context length",
	"prompt is too long", "max_tokens exceed context limit",
}

var clientPatterns = []string{
	"400 bad request", "404 not found", "410 gone",
	"not a valid model id",
	"no endpoints available matching your guardrail restrictions",
}
