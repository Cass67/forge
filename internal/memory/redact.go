package memory

import "regexp"

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization:\s*bearer\s+[A-Za-z0-9._\-]{16,}`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|secret)\s*[:=]\s*[^\s,;]+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`),
}

func RedactText(text string) string {
	redacted := text
	for _, pattern := range redactionPatterns {
		redacted = pattern.ReplaceAllString(redacted, "<REDACTED>")
	}
	return redacted
}
