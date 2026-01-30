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

	db, err := sql.Open("sqlite3", dbPath)
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

// RecordFix records a fix attempt (success or failure).
func (s *FixStore) RecordFix(pattern Pattern, fix string, success bool) error {
	// Try to update existing
	var result sql.Result
	var err error

	if success {
		result, err = s.db.Exec(`
			UPDATE fixes
			SET success_count = success_count + 1, last_used = CURRENT_TIMESTAMP
			WHERE command_pattern = ? AND error_pattern = ? AND exit_code = ? AND fix = ?
		`, pattern.CommandPattern, pattern.ErrorPattern, pattern.ExitCode, fix)
	} else {
		result, err = s.db.Exec(`
			UPDATE fixes
			SET failure_count = failure_count + 1, last_used = CURRENT_TIMESTAMP
			WHERE command_pattern = ? AND error_pattern = ? AND exit_code = ? AND fix = ?
		`, pattern.CommandPattern, pattern.ErrorPattern, pattern.ExitCode, fix)
	}

	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		return nil
	}

	// Insert new
	successCount := 0
	failureCount := 0
	if success {
		successCount = 1
	} else {
		failureCount = 1
	}

	_, err = s.db.Exec(`
		INSERT INTO fixes (command_pattern, error_pattern, exit_code, fix, success_count, failure_count)
		VALUES (?, ?, ?, ?, ?, ?)
	`, pattern.CommandPattern, pattern.ErrorPattern, pattern.ExitCode, fix, successCount, failureCount)

	return err
}

// GetFix retrieves the best fix for a pattern.
func (s *FixStore) GetFix(pattern Pattern) (Fix, bool) {
	row := s.db.QueryRow(`
		SELECT fix, success_count, failure_count, last_used
		FROM fixes
		WHERE command_pattern = ? AND error_pattern = ? AND exit_code = ?
		ORDER BY
			(success_count * 1.0 / (success_count + failure_count + 1)) DESC,
			success_count DESC
		LIMIT 1
	`, pattern.CommandPattern, pattern.ErrorPattern, pattern.ExitCode)

	var fix Fix
	fix.Pattern = pattern

	err := row.Scan(&fix.Fix, &fix.SuccessCount, &fix.FailureCount, &fix.LastUsed)
	if err == sql.ErrNoRows {
		return Fix{}, false
	}
	if err != nil {
		return Fix{}, false
	}

	// Calculate score
	fix.Score = calculateScore(fix.SuccessCount, fix.FailureCount, fix.LastUsed)

	return fix, true
}

// calculateScore computes the reliability score for a fix.
// score = (successRate * 0.5) + (recencyBoost * 0.3) + (frequencyBoost * 0.2)
func calculateScore(successCount, failureCount int, lastUsed time.Time) float64 {
	total := successCount + failureCount
	if total == 0 {
		return 0
	}

	// Success rate (0-1)
	successRate := float64(successCount) / float64(total)

	// Recency boost (0-1, decay over 30 days)
	daysSinceUse := time.Since(lastUsed).Hours() / 24
	recencyBoost := 1.0
	if daysSinceUse > 0 {
		recencyBoost = 1.0 / (1.0 + daysSinceUse/30.0)
	}

	// Frequency boost (0-1, based on total uses)
	frequencyBoost := float64(total) / (float64(total) + 10.0) // Asymptotic to 1

	return (successRate * 0.5) + (recencyBoost * 0.3) + (frequencyBoost * 0.2)
}

// PatternCount returns the number of unique patterns stored.
func (s *FixStore) PatternCount() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(DISTINCT pattern_hash) FROM fixes").Scan(&count)
	return count, err
}

// Close closes the database.
func (s *FixStore) Close() error {
	return s.db.Close()
}
