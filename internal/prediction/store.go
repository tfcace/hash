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

	CREATE TABLE IF NOT EXISTS path_usage (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		command   TEXT NOT NULL,
		path      TEXT NOT NULL,
		cwd       TEXT,
		count     INTEGER DEFAULT 1,
		last_used DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(command, path, cwd)
	);

	CREATE TABLE IF NOT EXISTS path_sequences (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		prev_command TEXT NOT NULL,
		path         TEXT NOT NULL,
		count        INTEGER DEFAULT 1,
		last_used    DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(prev_command, path)
	);

	CREATE INDEX IF NOT EXISTS idx_seq_prev ON command_sequences(prev_command);
	CREATE INDEX IF NOT EXISTS idx_path_cmd ON path_usage(command);
	CREATE INDEX IF NOT EXISTS idx_pathseq_prev ON path_sequences(prev_command);
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

// GetSequences returns sequences for a previous command.
func (s *Store) GetSequences(prevCmd, cwd string) ([]CommandSequence, error) {
	rows, err := s.db.Query(`
		SELECT id, prev_command, next_command, cwd_pattern, count, last_used
		FROM command_sequences
		WHERE prev_command = ? AND (cwd_pattern IS NULL OR cwd_pattern = ?)
		ORDER BY count DESC, last_used DESC
		LIMIT 10
	`, prevCmd, cwd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []CommandSequence
	for rows.Next() {
		var seq CommandSequence
		var cwdPattern sql.NullString
		err := rows.Scan(&seq.ID, &seq.PrevCommand, &seq.NextCommand, &cwdPattern, &seq.Count, &seq.LastUsed)
		if err != nil {
			return nil, err
		}
		seq.CwdPattern = cwdPattern.String
		seqs = append(seqs, seq)
	}
	return seqs, rows.Err()
}

// RecordPathUsage records a path usage with a command.
func (s *Store) RecordPathUsage(cmd, path, cwd string) error {
	_, err := s.db.Exec(`
		INSERT INTO path_usage (command, path, cwd, count, last_used)
		VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(command, path, cwd) DO UPDATE SET
			count = count + 1,
			last_used = CURRENT_TIMESTAMP
	`, cmd, path, cwd)
	return err
}

// GetPathsForCommand returns frequently used paths for a command.
func (s *Store) GetPathsForCommand(cmd, cwd string) ([]PathUsage, error) {
	rows, err := s.db.Query(`
		SELECT id, command, path, cwd, count, last_used
		FROM path_usage
		WHERE command = ? AND (cwd IS NULL OR cwd = ?)
		ORDER BY count DESC, last_used DESC
		LIMIT 10
	`, cmd, cwd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []PathUsage
	for rows.Next() {
		var pu PathUsage
		var cwdVal sql.NullString
		err := rows.Scan(&pu.ID, &pu.Command, &pu.Path, &cwdVal, &pu.Count, &pu.LastUsed)
		if err != nil {
			return nil, err
		}
		pu.Cwd = cwdVal.String
		paths = append(paths, pu)
	}
	return paths, rows.Err()
}

// RecordPathSequence records a path used after a command.
func (s *Store) RecordPathSequence(prevCmd, path string) error {
	_, err := s.db.Exec(`
		INSERT INTO path_sequences (prev_command, path, count, last_used)
		VALUES (?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(prev_command, path) DO UPDATE SET
			count = count + 1,
			last_used = CURRENT_TIMESTAMP
	`, prevCmd, path)
	return err
}

// GetPathsAfterCommand returns paths frequently used after a command.
func (s *Store) GetPathsAfterCommand(prevCmd string) ([]PathSequence, error) {
	rows, err := s.db.Query(`
		SELECT id, prev_command, path, count, last_used
		FROM path_sequences
		WHERE prev_command = ?
		ORDER BY count DESC, last_used DESC
		LIMIT 10
	`, prevCmd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []PathSequence
	for rows.Next() {
		var ps PathSequence
		err := rows.Scan(&ps.ID, &ps.PrevCommand, &ps.Path, &ps.Count, &ps.LastUsed)
		if err != nil {
			return nil, err
		}
		seqs = append(seqs, ps)
	}
	return seqs, rows.Err()
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}
