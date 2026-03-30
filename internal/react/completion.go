package react

import "strings"

type RetryableCompletionError struct {
	Message string
	Prompt  string
}

func (e *RetryableCompletionError) Error() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Message)
}

func NewRetryableCompletionError(message, prompt string) error {
	return &RetryableCompletionError{
		Message: strings.TrimSpace(message),
		Prompt:  strings.TrimSpace(prompt),
	}
}
