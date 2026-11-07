package prediction

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
)

// The SQL used to keep the 10 highest counts before recency was scored, so a
// recent, slightly less frequent command could never be predicted.
func TestPredictCommand_RecencyRanksBeyondTopTenByCount(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "prediction.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for i := 0; i < 10; i++ {
		for j := 0; j < 3; j++ {
			if err := store.RecordSequence("git", fmt.Sprintf("git old-%d", i), "/repo"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := store.db.Exec(`UPDATE command_sequences SET last_used = datetime('now', '-30 days')`); err != nil {
		t.Fatal(err)
	}
	for j := 0; j < 2; j++ {
		if err := store.RecordSequence("git", "git fresh", "/repo"); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.ConfidenceThreshold = 0.3
	if got := NewPredictor(store, cfg).PredictCommand("git status", "/repo"); got != "git fresh" {
		t.Errorf("PredictCommand() = %q, want git fresh (highest score)", got)
	}
}

// A recency window (path_recency_boost_hours) <= 0 divided by zero or flipped
// the sign: scores went infinite, NaN, negative, or above 1. A use in the
// future (clock skew) counts as now.
func TestRankSequences_ScoresStayInRangeForAnyRecencyWindow(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "prediction.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i, age := range []string{"+0 hours", "-12 hours", "-30 hours", "+1 hours"} {
		if _, err := store.db.Exec(`INSERT INTO command_sequences (prev_command, next_command, cwd_pattern, count, last_used)
			VALUES ('git', ?, '/repo', 3, datetime('now', ?))`, fmt.Sprintf("git %d", i), age); err != nil {
			t.Fatal(err)
		}
	}
	for _, hours := range []int{0, -24, 24} {
		seqs, err := store.RankSequences("git", "/repo", hours, 10)
		if err != nil || len(seqs) != 4 {
			t.Fatalf("hours=%d: RankSequences() = %d rows, %v", hours, len(seqs), err)
		}
		for _, seq := range seqs {
			if s := seq.Score; math.IsNaN(s) || math.IsInf(s, 0) || s < 0 || s > 1 {
				t.Errorf("hours=%d %s: score %v out of [0, 1]", hours, seq.NextCommand, s)
			}
		}
	}
}
