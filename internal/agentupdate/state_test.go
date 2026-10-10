package agentupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestState_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "agent-update.json")
	want := State{
		CheckedAt:       time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
		Latest:          "0.88.0",
		NotifiedAt:      time.Date(2026, 10, 9, 12, 0, 1, 0, time.UTC),
		NotifiedVersion: "0.88.0",
	}
	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	got := LoadState(path)
	if !got.CheckedAt.Equal(want.CheckedAt) || got.Latest != want.Latest ||
		!got.NotifiedAt.Equal(want.NotifiedAt) || got.NotifiedVersion != want.NotifiedVersion {
		t.Errorf("LoadState() = %+v, want %+v", got, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("state dir has %d entries, want just the state file (no temp leftovers)", len(entries))
	}
}

func TestState_MissingOrCorruptIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := LoadState(filepath.Join(dir, "missing.json")); got != (State{}) {
		t.Errorf("LoadState(missing) = %+v, want zero", got)
	}
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	if got := LoadState(corrupt); got != (State{}) {
		t.Errorf("LoadState(corrupt) = %+v, want zero", got)
	}
}
