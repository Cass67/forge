package react

import "testing"

func TestPromptToolResultBytesScalesWithWindow(t *testing.T) {
	cases := []struct{ tokens, want int }{
		{0, 8192},       // unknown window -> 32 KiB default inline / 4
		{40960, 8192},   // small window -> inline 32768/4
		{256000, 32000}, // 256k -> inline 128000/4
	}
	for _, c := range cases {
		got := promptToolResultBytes(0, func() int { return c.tokens })
		if got != c.want {
			t.Errorf("tokens=%d: got %d want %d", c.tokens, got, c.want)
		}
		if got < defaultPromptToolResultBytes {
			t.Errorf("tokens=%d: %d below floor", c.tokens, got)
		}
	}
}

func TestRunCommandExemptFromArgTruncation(t *testing.T) {
	if _, ok := authoredContentTools["run_command"]; !ok {
		t.Fatal("run_command must be exempt: a truncated command cannot be re-issued")
	}
}

func TestPromptTruncationLimitsScaleWithWindow(t *testing.T) {
	// Floors: an unknown or small window must behave exactly as before.
	if got := scaledToolResultMaxLines(0); got != toolResultMaxLines {
		t.Errorf("unknown window: lines %d, want floor %d", got, toolResultMaxLines)
	}
	if got := scaledToolCallArgSoftLimit(40960); got != toolCallArgSoftStringLimit {
		t.Errorf("small window: soft %d, want floor %d", got, toolCallArgSoftStringLimit)
	}
	// Large window must lift all three above their floors.
	const big = 256000
	if got := scaledToolResultMaxLines(big); got <= toolResultMaxLines {
		t.Errorf("256k: lines %d did not scale above %d", got, toolResultMaxLines)
	}
	if got := scaledToolCallArgSoftLimit(big); got <= toolCallArgSoftStringLimit {
		t.Errorf("256k: soft %d did not scale above %d", got, toolCallArgSoftStringLimit)
	}
	if soft, hard := scaledToolCallArgSoftLimit(big), scaledToolCallArgHardLimit(big); hard <= soft {
		t.Errorf("hard %d must stay above soft %d", hard, soft)
	}
}

func TestSessionCarriesContextWindowToSnapshot(t *testing.T) {
	s := NewSession()
	if got := s.Snapshot().ContextWindowTokens; got != 0 {
		t.Fatalf("unset window should be 0, got %d", got)
	}
	s.SetContextWindowFn(func() int { return 256000 })
	if got := s.Snapshot().ContextWindowTokens; got != 256000 {
		t.Fatalf("window not carried to snapshot: got %d", got)
	}
}
