package prompt

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Without starship on PATH the lookup is retried on every prompt. The first
// prompt and the palette are drawn concurrently at startup, and the retry
// used to write the path while the palette read it.
func TestStarshipLookup_ConcurrentRetryWhileMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := New(Config{Mode: "starship", StarshipPath: filepath.Join(t.TempDir(), "missing")})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); p.Generate(PromptContext{Cwd: "/"}) }()
		go func() { defer wg.Done(); p.GenerateForPalette(1) }()
	}
	wg.Wait()
}

// Once starship appears on PATH (a startup file extended it), the next
// prompt finds it, and the palette uses it without a separate resolve step.
func TestStarshipLookup_FoundLaterAndUsedByPalette(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	p := New(Config{Mode: "starship"})
	if got := p.GenerateForPalette(0); got != "" {
		t.Fatalf("GenerateForPalette() without starship = %q, want empty", got)
	}
	bin := filepath.Join(dir, "starship")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'STAR> '\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := p.GenerateForPalette(0); got != "STAR> " {
		t.Errorf("GenerateForPalette() after starship appeared = %q, want STAR> ", got)
	}
	if got := p.Generate(PromptContext{Cwd: "/"}); got != "STAR> " {
		t.Errorf("Generate() = %q, want the starship prompt", got)
	}
}
