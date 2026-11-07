package prediction

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func seededStore(tb testing.TB, n int) *Store {
	tb.Helper()
	store, err := NewStore(filepath.Join(tb.TempDir(), "prediction.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = store.Close() })
	tx, err := store.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT INTO command_sequences (prev_command, next_command, cwd_pattern, count, last_used)
			VALUES ('git', ?, '/repo', ?, datetime('now', ?))`, fmt.Sprintf("git cmd-%d", i), 1+i%7, fmt.Sprintf("-%d hours", i%500)); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return store
}

// PredictCommand runs before every prompt; it must not read every sequence
// ever recorded after the last command (17.6ms per call at 20k rows).
func TestRankSequences_ReadsOnlyTheBestRows(t *testing.T) {
	store := seededStore(t, 200)
	seqs, err := store.RankSequences("git", "/repo", 24, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 3 {
		t.Fatalf("RankSequences read %d rows, want the 3 best", len(seqs))
	}
	// The SQL ranking must match the documented formula.
	for i, seq := range seqs {
		age := time.Since(seq.LastUsed).Hours()
		want := float64(seq.Count)/(float64(seq.Count)+5)*0.7 + 0.3/(1+age/24)
		if math.Abs(seq.Score-want) > 1e-3 {
			t.Errorf("seq %d (%d uses, %.1fh ago) score %.4f, want %.4f", i, seq.Count, age, seq.Score, want)
		}
		if i > 0 && seq.Score > seqs[i-1].Score {
			t.Errorf("seq %d scores above seq %d", i, i-1)
		}
	}
}

func BenchmarkPredictCommand20k(b *testing.B) {
	p := NewPredictor(seededStore(b, 20000), DefaultConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.PredictCommand("git status", "/repo")
	}
}
