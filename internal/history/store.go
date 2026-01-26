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
	db *sql.DB
}

// NewStore creates or opens a history database.
func NewStore(dbPath string) (*Store, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
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
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY c.timestamp DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", opts.Offset)
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

// Count returns the total number of commands in history.
func (s *Store) Count() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM history").Scan(&count)
	return count, err
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
