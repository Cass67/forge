package runtime

import "testing"

func TestIsLocalBaseURL(t *testing.T) {
	cases := map[string]bool{
		"http://192.168.2.1:3001/v1": true,
		"http://localhost:11434/v1":  true,
		"http://127.0.0.1:8080/v1":   true,
		"http://mymac.local:1234/v1": true,
		"https://api.openai.com/v1":  false,
		"https://api.x.ai/v1":        false,
		"":                           false,
	}
	for raw, want := range cases {
		if got := isLocalBaseURL(raw); got != want {
			t.Errorf("isLocalBaseURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
