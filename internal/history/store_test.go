package history

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_AgentInteractionsSearchPromptAndResponse(t *testing.T) {
	store := newTestStore(t)
	now := time.Now()
	for _, interaction := range []AgentInteraction{
		{Prompt: "find logs", Response: "rg --files | rg log", ResponseKind: AgentResponseKindCommand, Timestamp: now},
		{Prompt: "explain deploy", Response: "The deployment is blocked by a pending migration.", ResponseKind: AgentResponseKindExplanation, Timestamp: now.Add(time.Second)},
	} {
		if _, err := store.AddAgentInteraction(interaction); err != nil {
			t.Fatalf("AddAgentInteraction() error = %v", err)
		}
	}

	byPrompt, err := store.GetAgentInteractions("logs", 20)
	if err != nil {
		t.Fatalf("GetAgentInteractions(prompt) error = %v", err)
	}
	if len(byPrompt) != 1 || byPrompt[0].ResponseKind != AgentResponseKindCommand {
		t.Fatalf("prompt search = %#v, want command result", byPrompt)
	}

	byResponse, err := store.GetAgentInteractions("pending migration", 20)
	if err != nil {
		t.Fatalf("GetAgentInteractions(response) error = %v", err)
	}
	if len(byResponse) != 1 || byResponse[0].ResponseKind != AgentResponseKindExplanation {
		t.Fatalf("response search = %#v, want explanation result", byResponse)
	}
}

func TestStore_AgentInteractionsOrderByRecencyAndHonorLimit(t *testing.T) {
	store := newTestStore(t)
	base := time.Now().Add(-time.Hour)
	for i, interaction := range []AgentInteraction{
		{Prompt: "old", Response: "old response", Timestamp: base},
		{Prompt: "middle", Response: "middle response", Timestamp: base.Add(time.Minute)},
		{Prompt: "new", Response: "new response", Timestamp: base.Add(2 * time.Minute)},
	} {
		if _, err := store.AddAgentInteraction(interaction); err != nil {
			t.Fatalf("AddAgentInteraction(%d) error = %v", i, err)
		}
	}

	interactions, err := store.GetAgentInteractions("", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 2 {
		t.Fatalf("GetAgentInteractions limit = %d, want 2", len(interactions))
	}
	if interactions[0].Prompt != "new" || interactions[1].Prompt != "middle" {
		t.Fatalf("recency order = [%q, %q], want [new, middle]", interactions[0].Prompt, interactions[1].Prompt)
	}
}

func TestStore_LegacyAgentInteractionsMigrateAsUnknown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE agent_interactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		prompt TEXT NOT NULL,
		response TEXT NOT NULL,
		accepted BOOLEAN DEFAULT FALSE,
		command_id INTEGER,
		context TEXT,
		latency_ms INTEGER DEFAULT 0,
		agent TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	); INSERT INTO agent_interactions (prompt, response) VALUES ('legacy prompt', 'legacy response');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	defer store.Close()

	interactions, err := store.GetAgentInteractions("legacy", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 || interactions[0].ResponseKind != AgentResponseKindUnknown {
		t.Fatalf("legacy interaction = %#v, want unknown response kind", interactions)
	}
}

func TestStore_CreateAndAdd(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cmd := Command{
		Command:    "echo hello",
		Cwd:        "/home/user",
		ExitCode:   0,
		DurationMs: 10,
		Timestamp:  time.Now(),
	}

	id, err := store.Add(cmd)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if id <= 0 {
		t.Errorf("ID = %d, want > 0", id)
	}
}

func TestStore_GetRecent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Add some commands
	for i := 0; i < 5; i++ {
		store.Add(Command{
			Command:   "echo " + string(rune('a'+i)),
			Cwd:       "/home/user",
			Timestamp: time.Now(),
		})
	}

	commands, err := store.GetRecent(3)
	if err != nil {
		t.Fatalf("GetRecent() error = %v", err)
	}

	if len(commands) != 3 {
		t.Errorf("Count = %d, want 3", len(commands))
	}

	// Most recent should be first
	if commands[0].Command != "echo e" {
		t.Errorf("First = %q, want %q", commands[0].Command, "echo e")
	}
}

func TestStore_Search(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	store.Add(Command{Command: "kubectl get pods", Cwd: "/", Timestamp: time.Now()})
	store.Add(Command{Command: "docker ps", Cwd: "/", Timestamp: time.Now()})
	store.Add(Command{Command: "kubectl get services", Cwd: "/", Timestamp: time.Now()})

	results, err := store.Search(SearchOptions{Query: "kubectl"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Count = %d, want 2", len(results))
	}
}

func TestStore_SudoCommands(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	store.Add(Command{
		Command:    "apt-get update",
		IsSudo:     true,
		SudoUser:   "root",
		RawCommand: "sudo apt-get update",
		Cwd:        "/",
		Timestamp:  time.Now(),
	})
	store.Add(Command{Command: "ls", Cwd: "/", Timestamp: time.Now()})

	results, err := store.Search(SearchOptions{OnlySudo: true})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Count = %d, want 1", len(results))
	}
	if !results[0].IsSudo {
		t.Error("Expected sudo command")
	}
}

func TestStore_FileCreated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSearchByPrefix_MatchesPrefix(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "git push origin main", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "docker ps", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestSearchByPrefix_ExcludesFailed(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git push", Cwd: "/", ExitCode: 1, Timestamp: time.Now()})
	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (only successful)", len(results))
	}
	if results[0] != "git status" {
		t.Errorf("got %q, want %q", results[0], "git status")
	}
}

func TestSearchByPrefix_Deduplicates(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (deduped)", len(results))
	}
}

func TestSearchByPrefix_OrdersByRecent(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now().Add(-time.Hour)})
	store.Add(Command{Command: "git push", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0] != "git push" {
		t.Errorf("first result = %q, want %q (most recent)", results[0], "git push")
	}
}

func TestSearchByPrefix_EmptyPrefix(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty prefix, got %v", results)
	}
}

func TestSearchByPrefix_GlobEscaping(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "echo *", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "echo hello", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	// Searching for "echo *" should match literally, not as a glob wildcard
	results, err := store.SearchByPrefix("echo *", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0] != "echo *" {
		t.Errorf("got %q, want %q", results[0], "echo *")
	}
}

func TestSearchByPrefix_NoMatch(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("docker", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}
