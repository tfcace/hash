package shell

import (
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/prediction"
)

// BenchmarkShellNew measures the full shell initialization time.
// This is the startup latency a user experiences when opening a new terminal.
func BenchmarkShellNew(b *testing.B) {
	cfg := config.Default()
	// Disable starship to isolate DB + setup overhead
	cfg.Prompt.Mode = "built-in"
	// Disable prediction to avoid creating a third database
	cfg.Prediction.Enabled = false

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sh, err := New(cfg)
		if err != nil {
			b.Fatalf("New() error = %v", err)
		}
		sh.Close()
	}
}

// BenchmarkShellNewWithPrediction measures shell init with all three databases.
func BenchmarkShellNewWithPrediction(b *testing.B) {
	cfg := config.Default()
	cfg.Prompt.Mode = "built-in"
	cfg.Prediction.Enabled = true

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sh, err := New(cfg)
		if err != nil {
			b.Fatalf("New() error = %v", err)
		}
		sh.Close()
	}
}

// BenchmarkDBInitSequential measures opening all three databases sequentially.
// This was the original behavior before parallelization.
func BenchmarkDBInitSequential(b *testing.B) {
	dir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h, err := history.NewStore(filepath.Join(dir, "history.db"))
		if err != nil {
			b.Fatal(err)
		}

		l, err := learning.NewFixStore(filepath.Join(dir, "learning.db"))
		if err != nil {
			b.Fatal(err)
		}

		p, err := prediction.NewStore(filepath.Join(dir, "prediction.db"))
		if err != nil {
			b.Fatal(err)
		}

		h.Close()
		l.Close()
		p.Close()
	}
}

// BenchmarkDBInitParallel measures opening all three databases concurrently.
// This is the new behavior — goroutines overlap the I/O and migration work.
func BenchmarkDBInitParallel(b *testing.B) {
	dir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		type result struct {
			closer interface{ Close() error }
			err    error
		}
		ch1 := make(chan result, 1)
		ch2 := make(chan result, 1)
		ch3 := make(chan result, 1)

		go func() {
			s, err := history.NewStore(filepath.Join(dir, "history.db"))
			ch1 <- result{s, err}
		}()
		go func() {
			s, err := learning.NewFixStore(filepath.Join(dir, "learning.db"))
			ch2 <- result{s, err}
		}()
		go func() {
			s, err := prediction.NewStore(filepath.Join(dir, "prediction.db"))
			ch3 <- result{s, err}
		}()

		r1 := <-ch1
		r2 := <-ch2
		r3 := <-ch3

		if r1.err != nil {
			b.Fatal(r1.err)
		}
		if r2.err != nil {
			b.Fatal(r2.err)
		}
		if r3.err != nil {
			b.Fatal(r3.err)
		}

		r1.closer.Close()
		r2.closer.Close()
		r3.closer.Close()
	}
}
