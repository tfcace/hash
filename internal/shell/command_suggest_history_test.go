package shell

import (
	"testing"
	"time"

	"github.com/tfcace/hash/internal/history"
)

// A typo that failed last time is not a correction for the next typo.
func TestSuggest_IgnoresCommandsThatFailed(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for _, c := range []history.Command{
		{Command: "osuchcmd", ExitCode: 127, Timestamp: now, Cwd: "/tmp"},
		{Command: "ls -la", ExitCode: 0, Timestamp: now, Cwd: "/tmp"},
	} {
		if _, err := store.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	s := &CommandSuggestor{historyStore: store}

	for _, got := range s.Suggest("nosuchcmd") {
		if got == "osuchcmd" {
			t.Fatalf("Suggest(nosuchcmd) offered the failed command %q", got)
		}
	}
	if got := s.Suggest("sl"); len(got) == 0 || got[0] != "ls" {
		t.Errorf("Suggest(sl) = %v, want the successful command ls", got)
	}
}

// A history line whose name touches a separator ("wwkk; echo b", "ls|wc")
// offers the name alone: "wwkk;" is no command, and offering it as the
// correction for wwkk puts the typo back in the corrected line.
func TestSuggest_HistoryNameEndsAtASeparator(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for _, c := range []history.Command{
		{Command: "wwkk; echo b", ExitCode: 0, Timestamp: now, Cwd: "/tmp"},
		{Command: "ls|wc -l", ExitCode: 0, Timestamp: now, Cwd: "/tmp"},
	} {
		if _, err := store.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	s := &CommandSuggestor{historyStore: store}

	if got := s.Suggest("wwkk"); len(got) != 0 {
		t.Errorf("Suggest(wwkk) = %v, want nothing (the history name is wwkk itself)", got)
	}
	if got := s.Suggest("sl"); len(got) == 0 || got[0] != "ls" {
		t.Errorf("Suggest(sl) = %v, want ls from \"ls|wc -l\"", got)
	}
}
