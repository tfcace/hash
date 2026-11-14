package prediction

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Store provides persistent storage for prediction data.
type Store struct {
	db *sql.DB
}

// NewStore creates or opens a prediction database.
func NewStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: standard user data dir perms
		return nil, fmt.Errorf("create prediction dir: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return store, nil
}

// migrate creates the schema. Databases written by older versions also hold
// path_usage and path_sequences tables for path prediction, which was never
// wired up; they are left in place (an older hash on the same file would
// recreate them anyway) and no longer read or written.
func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS command_sequences (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		prev_command TEXT NOT NULL,
		next_command TEXT NOT NULL,
		cwd_pattern  TEXT,
		count        INTEGER DEFAULT 1,
		last_used    DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(prev_command, next_command, cwd_pattern)
	);

	CREATE INDEX IF NOT EXISTS idx_seq_prev ON command_sequences(prev_command);
	`
	_, err := s.db.Exec(schema)
	return err
}

// RecordSequence records a command sequence.
func (s *Store) RecordSequence(prevCmd, nextCmd, cwd string) error {
	_, err := s.db.Exec(`
		INSERT INTO command_sequences (prev_command, next_command, cwd_pattern, count, last_used)
		VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(prev_command, next_command, cwd_pattern) DO UPDATE SET
			count = count + 1,
			last_used = CURRENT_TIMESTAMP
	`, prevCmd, nextCmd, cwd)
	return err
}

// RankedSequence is a recorded sequence with its prediction score.
type RankedSequence struct {
	CommandSequence
	Score float64
}

// RankSequences returns the limit best-scored sequences after prevCmd in cwd.
// The score is ranked in SQL, so only those rows are read: a frequency share
// count/(count+5) weighted 0.7 plus a recency 1/(1+hours/windowHours) weighted
// 0.3. A window <= 0 falls back to the default, and a use in the future
// (clock skew) counts as now, so scores stay within [0, 1].
func (s *Store) RankSequences(prevCmd, cwd string, windowHours, limit int) ([]RankedSequence, error) {
	if windowHours <= 0 {
		windowHours = DefaultConfig().RecencyWindowHours
	}
	rows, err := s.db.Query(`
		SELECT id, prev_command, next_command, cwd_pattern, count, last_used,
			count * 1.0 / (count + 5.0) * 0.7
			+ 0.3 / (1.0 + MAX(0.0, (julianday('now') - julianday(last_used)) * 24.0) / ?) AS score
		FROM command_sequences
		WHERE prev_command = ? AND cwd_pattern = ?
		ORDER BY score DESC, last_used DESC
		LIMIT ?
	`, windowHours, prevCmd, cwd, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []RankedSequence
	for rows.Next() {
		var seq RankedSequence
		var cwdPattern sql.NullString
		err := rows.Scan(&seq.ID, &seq.PrevCommand, &seq.NextCommand, &cwdPattern, &seq.Count, &seq.LastUsed, &seq.Score)
		if err != nil {
			return nil, err
		}
		seq.CwdPattern = cwdPattern.String
		seqs = append(seqs, seq)
	}
	return seqs, rows.Err()
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}
