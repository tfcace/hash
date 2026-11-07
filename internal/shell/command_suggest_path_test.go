package shell

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// rc files change PATH after the suggestor starts; a typo of a command they
// put on PATH is still corrected.
func TestCommandSuggestor_UsesThePATHOfTheTypo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := NewCommandSuggestor(nil)
	deadline := time.Now().Add(2 * time.Second)
	for !s.pathCacheReady.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "frobnicate"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	got := s.Suggest("frobnicat")
	if len(got) == 0 || got[0] != "frobnicate" {
		t.Errorf("Suggest(frobnicat) = %v, want frobnicate from the current PATH", got)
	}
}

func TestCommandSuggestor_PackageManagerDetectionIsRaceFree(t *testing.T) {
	s := &CommandSuggestor{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.InstallHint("jq")
		}()
	}
	wg.Wait()
}

// fixedSuggestor suggests from names alone, whatever PATH holds.
func fixedSuggestor(names ...string) *CommandSuggestor {
	s := &CommandSuggestor{pathCache: names, pathFor: "fixed", pathFunc: func() string { return "fixed" }}
	s.pathCacheReady.Store(true)
	return s
}

// A typo is corrected only to a command name worth suggesting: `[` and
// `[[` (on PATH, or first on a line of history) are one edit from `c`,
// but no one meant them.
func TestCommandSuggestor_SuggestsNoPunctuationName(t *testing.T) {
	s := fixedSuggestor("[", "[[", ":", ".", "cc")
	if got := s.Suggest("c"); len(got) != 1 || got[0] != "cc" {
		t.Errorf("Suggest(c) = %v, want [cc]", got)
	}
}
