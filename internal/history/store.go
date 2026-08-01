package history

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Store provides persistent command history.
type Store struct {
	db      *sql.DB
	idx     *prefixIndex
	idxDone chan struct{} // closed when the background index load finishes
}

// NewStore creates or opens a history database.
func NewStore(dbPath string) (*Store, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: standard user data dir
		return nil, fmt.Errorf("create history dir: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// A plain in-memory DSN gives every pooled connection its own empty
	// database, so the background index load (the first concurrent user of
	// this pool) would see missing tables. Cap the pool at the one real
	// connection; file-backed databases keep normal pooling under WAL.
	if strings.Contains(dbPath, ":memory:") {
		db.SetMaxOpenConns(1)
	}

	store := &Store{db: db, idx: &prefixIndex{}, idxDone: make(chan struct{})}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Load the prefix index off the startup path: SearchByPrefix answers from
	// SQL until the load lands, then from memory for the rest of the session.
	go func() {
		defer close(store.idxDone)
		store.loadPrefixIndex()
	}()

	return store, nil
}

// migrate runs database migrations.
func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS commands (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		command     TEXT NOT NULL,
		cwd         TEXT,
		exit_code   INTEGER DEFAULT 0,
		duration_ms INTEGER DEFAULT 0,
		timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP,
		git_branch  TEXT,
		kube_context TEXT,
		is_sudo     BOOLEAN DEFAULT FALSE,
		sudo_user   TEXT,
		raw_command TEXT
	);

	CREATE TABLE IF NOT EXISTS agent_interactions (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		prompt      TEXT NOT NULL,
		response    TEXT NOT NULL,
		accepted    BOOLEAN DEFAULT FALSE,
		command_id  INTEGER REFERENCES commands(id),
		context     TEXT,
		latency_ms  INTEGER DEFAULT 0,
		agent       TEXT,
		timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_commands_timestamp ON commands(timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_commands_command ON commands(command);
	CREATE INDEX IF NOT EXISTS idx_commands_is_sudo ON commands(is_sudo);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Add inserts a command into history.
func (s *Store) Add(cmd Command) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO commands (command, cwd, exit_code, duration_ms, timestamp, git_branch, kube_context, is_sudo, sudo_user, raw_command)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, cmd.Command, cmd.Cwd, cmd.ExitCode, cmd.DurationMs, cmd.Timestamp, cmd.GitBranch, cmd.KubeContext, cmd.IsSudo, cmd.SudoUser, cmd.RawCommand)

	if err != nil {
		return 0, err
	}

	if cmd.ExitCode == 0 && s.idx != nil {
		s.idx.record(cmd.Command)
	}

	return result.LastInsertId()
}

// GetRecent returns the N most recent commands.
func (s *Store) GetRecent(n int) ([]Command, error) {
	rows, err := s.db.Query(`
		SELECT id, command, cwd, exit_code, duration_ms, timestamp, git_branch, kube_context, is_sudo, sudo_user, raw_command
		FROM commands
		ORDER BY timestamp DESC
		LIMIT ?
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanCommands(rows)
}

// Search performs a search on command history.
func (s *Store) Search(opts SearchOptions) ([]Command, error) {
	var conditions []string
	var args []interface{}

	query := `
		SELECT c.id, c.command, c.cwd, c.exit_code, c.duration_ms, c.timestamp, c.git_branch, c.kube_context, c.is_sudo, c.sudo_user, c.raw_command
		FROM commands c
	`

	// LIKE-based search (universal compatibility)
	if opts.Query != "" {
		conditions = append(conditions, "c.command LIKE ?")
		args = append(args, "%"+opts.Query+"%")
	}

	if opts.OnlyFailed {
		conditions = append(conditions, "c.exit_code != 0")
	}

	if opts.OnlySudo {
		conditions = append(conditions, "c.is_sudo = TRUE")
	}

	if opts.Cwd != "" {
		conditions = append(conditions, "c.cwd = ?")
		args = append(args, opts.Cwd)
	}

	if !opts.Since.IsZero() {
		conditions = append(conditions, "c.timestamp >= ?")
		args = append(args, opts.Since)
	}

	if !opts.Before.IsZero() {
		conditions = append(conditions, "c.timestamp < ?")
		args = append(args, opts.Before)
	}

	if len(conditions) > 0 {
		// conditions holds only hardcoded SQL fragments; every user value is
		// bound through a ? placeholder in args, so this cannot be injected.
		query += " WHERE " + strings.Join(conditions, " AND ") //nolint:gosec // G202: conditions are constant fragments, values are parameterized
	}

	query += " ORDER BY c.timestamp DESC"

	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	if opts.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, opts.Offset)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanCommands(rows)
}

// scanCommands reads rows into Command structs.
func (s *Store) scanCommands(rows *sql.Rows) ([]Command, error) {
	var commands []Command

	for rows.Next() {
		var cmd Command
		var gitBranch, kubeContext, sudoUser, rawCommand sql.NullString

		err := rows.Scan(
			&cmd.ID, &cmd.Command, &cmd.Cwd, &cmd.ExitCode, &cmd.DurationMs,
			&cmd.Timestamp, &gitBranch, &kubeContext, &cmd.IsSudo, &sudoUser, &rawCommand,
		)
		if err != nil {
			return nil, err
		}

		cmd.GitBranch = gitBranch.String
		cmd.KubeContext = kubeContext.String
		cmd.SudoUser = sudoUser.String
		cmd.RawCommand = rawCommand.String

		commands = append(commands, cmd)
	}

	return commands, rows.Err()
}

// AddAgentInteraction records an agent interaction.
func (s *Store) AddAgentInteraction(interaction AgentInteraction) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO agent_interactions (prompt, response, accepted, command_id, context, latency_ms, agent, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, interaction.Prompt, interaction.Response, interaction.Accepted, interaction.CommandID, interaction.Context, interaction.LatencyMs, interaction.Agent, interaction.Timestamp)

	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// GetAgentInteractions returns agent interactions, optionally filtered.
func (s *Store) GetAgentInteractions(prompt string, limit int) ([]AgentInteraction, error) {
	var rows *sql.Rows
	var err error

	if prompt != "" {
		rows, err = s.db.Query(`
			SELECT id, prompt, response, accepted, command_id, context, latency_ms, agent, timestamp
			FROM agent_interactions
			WHERE prompt LIKE ?
			ORDER BY timestamp DESC
			LIMIT ?
		`, "%"+prompt+"%", limit)
	} else {
		rows, err = s.db.Query(`
			SELECT id, prompt, response, accepted, command_id, context, latency_ms, agent, timestamp
			FROM agent_interactions
			ORDER BY timestamp DESC
			LIMIT ?
		`, limit)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var interactions []AgentInteraction
	for rows.Next() {
		var i AgentInteraction
		var commandID sql.NullInt64
		var context sql.NullString

		err := rows.Scan(&i.ID, &i.Prompt, &i.Response, &i.Accepted, &commandID, &context, &i.LatencyMs, &i.Agent, &i.Timestamp)
		if err != nil {
			return nil, err
		}

		i.CommandID = commandID.Int64
		i.Context = context.String
		interactions = append(interactions, i)
	}

	return interactions, rows.Err()
}

// SearchByPrefix returns the most recent successful commands matching a prefix.
// The results are deduplicated (by command text) and ordered by most recent first.
// Returns nil for empty prefix or if no matches are found.
//
// This runs on every keystroke (ghost-text suggestions), so once the in-memory
// prefix index has loaded it answers from there; SQL is only the fallback for
// the brief window before the background load completes.
func (s *Store) SearchByPrefix(prefix string, limit int) ([]string, error) {
	if prefix == "" {
		return nil, nil
	}

	if s.idx != nil {
		if cmds, ok := s.idx.search(prefix, limit); ok {
			return cmds, nil
		}
	}

	escaped := escapeGlob(prefix)
	rows, err := s.db.Query(`
		SELECT command, MAX(timestamp) as latest
		FROM commands
		WHERE command GLOB ? AND exit_code = 0
		GROUP BY command
		ORDER BY latest DESC
		LIMIT ?
	`, escaped+"*", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []string
	for rows.Next() {
		var cmd string
		var ts interface{}
		if err := rows.Scan(&cmd, &ts); err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}
	return commands, rows.Err()
}

// loadPrefixIndex runs the distinct-successful-commands aggregation once and
// hands the result to the in-memory index. On any error the index simply
// stays unloaded and SearchByPrefix keeps answering from SQL, so a failure
// here only costs speed, never correctness.
func (s *Store) loadPrefixIndex() {
	rows, err := s.db.Query(`
		SELECT command, MAX(timestamp) as latest
		FROM commands
		WHERE exit_code = 0
		GROUP BY command
		ORDER BY latest DESC
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	var cmds []string
	for rows.Next() {
		var cmd string
		var latest interface{}
		if err := rows.Scan(&cmd, &latest); err != nil {
			return
		}
		cmds = append(cmds, cmd)
	}
	if rows.Err() != nil {
		return
	}
	s.idx.install(cmds)
}

// waitPrefixIndex blocks until the background index load has finished and
// reports whether the index is serving lookups. Test and benchmark helper.
func (s *Store) waitPrefixIndex() bool {
	<-s.idxDone
	s.idx.mu.RLock()
	defer s.idx.mu.RUnlock()
	return s.idx.loaded
}

// escapeGlob escapes special glob characters in a string for safe use in GLOB queries.
func escapeGlob(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']':
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Count returns the total number of commands in history.
func (s *Store) Count() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM commands").Scan(&count)
	return count, err
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
