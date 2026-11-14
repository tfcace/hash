package learning

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Fix represents a learned fix for an error pattern.
type Fix struct {
	Pattern      Pattern
	Fix          string  // The command/action that fixed it
	Score        float64 // 0-1, higher = more reliable
	SuccessCount int
	FailureCount int
	LastUsed     time.Time
}

// FixStore stores learned error patterns and their fixes.
type FixStore struct {
	db *sql.DB
}

// NewFixStore creates or opens a learning database.
func NewFixStore(dbPath string) (*FixStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: standard user data dir perms
		return nil, fmt.Errorf("create learning dir: %w", err)
	}

	// Several shells share this database: wait for a writer instead of
	// failing with "database is locked".
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &FixStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return store, nil
}

func (s *FixStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS fixes (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		command_pattern TEXT NOT NULL,
		error_pattern   TEXT NOT NULL,
		exit_code       INTEGER NOT NULL,
		fix             TEXT NOT NULL,
		success_count   INTEGER DEFAULT 0,
		failure_count   INTEGER DEFAULT 0,
		last_used       DATETIME DEFAULT CURRENT_TIMESTAMP,
		created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(command_pattern, error_pattern, exit_code, fix)
	);

	CREATE INDEX IF NOT EXISTS idx_fixes_patterns ON fixes(command_pattern, error_pattern, exit_code);
	`

	_, err := s.db.Exec(schema)
	return err
}

// RecordFix records a fix attempt (success or failure). It is a single
// upsert, so shells sharing the database cannot race between an update that
// found no row and an insert that hits the UNIQUE constraint.
func (s *FixStore) RecordFix(pattern Pattern, fix string, success bool) error {
	successCount, failureCount := 0, 1
	if success {
		successCount, failureCount = 1, 0
	}
	_, err := s.db.Exec(`
		INSERT INTO fixes (command_pattern, error_pattern, exit_code, fix, success_count, failure_count)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(command_pattern, error_pattern, exit_code, fix) DO UPDATE SET
			success_count = success_count + excluded.success_count,
			failure_count = failure_count + excluded.failure_count,
			last_used = CURRENT_TIMESTAMP
	`, pattern.CommandPattern, pattern.ErrorPattern, pattern.ExitCode, fix, successCount, failureCount)
	return err
}

// GetFix retrieves the highest-scoring fix for a pattern.
func (s *FixStore) GetFix(pattern Pattern) (Fix, bool) {
	return s.bestFix(pattern, func(string) bool { return true })
}

// GetRelatedFix retrieves the highest-scoring fix for a pattern that is
// Related to the failed command, skipping better-scored fixes that are not.
func (s *FixStore) GetRelatedFix(pattern Pattern, failed string) (Fix, bool) {
	return s.bestFix(pattern, func(fix string) bool { return Related(failed, fix) })
}

// bestFix ranks every accepted candidate with calculateScore, the same score
// callers compare against their thresholds.
func (s *FixStore) bestFix(pattern Pattern, accept func(fix string) bool) (Fix, bool) {
	rows, err := s.db.Query(`
		SELECT fix, success_count, failure_count, last_used
		FROM fixes
		WHERE command_pattern = ? AND error_pattern = ? AND exit_code = ?
	`, pattern.CommandPattern, pattern.ErrorPattern, pattern.ExitCode)
	if err != nil {
		return Fix{}, false
	}
	defer rows.Close()

	var best Fix
	found := false
	for rows.Next() {
		fix := Fix{Pattern: pattern}
		if err := rows.Scan(&fix.Fix, &fix.SuccessCount, &fix.FailureCount, &fix.LastUsed); err != nil {
			return Fix{}, false
		}
		if !accept(fix.Fix) {
			continue
		}
		fix.Score = calculateScore(fix.SuccessCount, fix.FailureCount, fix.LastUsed)
		if !found || betterFix(fix, best) {
			best, found = fix, true
		}
	}
	if rows.Err() != nil {
		return Fix{}, false
	}
	return best, found
}

// betterFix orders by score, then success count, then fix text so the
// choice is deterministic.
func betterFix(a, b Fix) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.SuccessCount != b.SuccessCount {
		return a.SuccessCount > b.SuccessCount
	}
	return a.Fix < b.Fix
}

// calculateScore computes the reliability score for a fix.
// score = (smoothedRate * 0.65) + (recencyBoost * sizeConfidence * 0.25) + (frequencyBoost * 0.10)
//
// The success rate is Laplace-smoothed and the recency boost is scaled by
// sample size so that a single fresh success lands in the tentative band
// [0.5, 0.7) instead of clearing the 0.7 high-confidence bar, while a fix
// that fails more than it works drops below the 0.5 suggestion floor.
func calculateScore(successCount, failureCount int, lastUsed time.Time) float64 {
	total := successCount + failureCount
	if total == 0 {
		return 0
	}

	// Smoothed success rate (0-1): pulls small samples toward 0.5
	smoothedRate := (float64(successCount) + 0.5) / (float64(total) + 1.0)

	// Recency boost (0-1, decay over 30 days)
	daysSinceUse := time.Since(lastUsed).Hours() / 24
	recencyBoost := 1.0
	if daysSinceUse > 0 {
		recencyBoost = 1.0 / (1.0 + daysSinceUse/30.0)
	}

	// Sample-size confidence (0-1): recency alone can't carry one observation
	sizeConfidence := float64(total) / (float64(total) + 1.0)

	// Frequency boost (0-1, based on total uses)
	frequencyBoost := float64(total) / (float64(total) + 10.0) // Asymptotic to 1

	return (smoothedRate * 0.65) + (recencyBoost * sizeConfidence * 0.25) + (frequencyBoost * 0.10)
}

// PatternCount returns the number of unique patterns stored.
func (s *FixStore) PatternCount() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(DISTINCT command_pattern || '|' || error_pattern) FROM fixes").Scan(&count)
	return count, err
}

// Close closes the database.
func (s *FixStore) Close() error {
	return s.db.Close()
}
