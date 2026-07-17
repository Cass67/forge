package memory

import "testing"

func TestPersistRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if got := LoadState("/some/workdir"); len(got.Records) != 0 {
		t.Fatalf("expected empty state, got %+v", got)
	}
	want := State{
		Records: []Record{{Mode: "build", Objective: "fix bug", Summary: "fixed the bug"}},
		Summary: "- fixed the bug",
	}
	if err := SaveState("/some/workdir", want); err != nil {
		t.Fatal(err)
	}
	got := LoadState("/some/workdir")
	if got.Summary != want.Summary || len(got.Records) != 1 || got.Records[0] != want.Records[0] {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(LoadState("/other/workdir").Records) != 0 {
		t.Fatal("workdirs should not share state")
	}
}
