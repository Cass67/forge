package memory

import "regexp"

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

func RedactText(text string) string {
	redacted := text
	for _, pattern := range redactionPatterns {
		redacted = pattern.ReplaceAllString(redacted, "<REDACTED>")
	}
	return redacted
}
