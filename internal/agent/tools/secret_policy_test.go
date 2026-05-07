package tools

import "strings"

func dummySecret() string {
	return "ghp_" + strings.Repeat("a", 36)
}
