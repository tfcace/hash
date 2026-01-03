package history

import "time"

// Command represents a command in history.
type Command struct {
	ID          int64
	Command     string    // The command that was executed
	Cwd         string    // Working directory
	ExitCode    int       // Exit code (0 = success)
	DurationMs  int64     // Execution duration in milliseconds
	Timestamp   time.Time // When the command was executed
	GitBranch   string    // Git branch at time of execution
	KubeContext string    // Kubernetes context
	IsSudo      bool      // Was this a sudo command?
	SudoUser    string    // User switched to (usually "root")
	RawCommand  string    // Original command with sudo prefix
}

// AgentInteraction represents an agent prompt/response pair.
type AgentInteraction struct {
	ID        int64
	Prompt    string    // What the user asked
	Response  string    // What the agent suggested
	Accepted  bool      // Did the user accept the suggestion?
	CommandID int64     // Link to executed command (if accepted)
	Context   string    // Context that was sent (JSON)
	LatencyMs int64     // Agent response time
	Agent     string    // Which agent (claude, ollama, etc.)
	Timestamp time.Time
}

// SearchOptions for querying history.
type SearchOptions struct {
	Query      string // Full-text search query
	Limit      int    // Max results (0 = unlimited)
	Offset     int    // Pagination offset
	OnlyFailed bool   // Only show failed commands
	OnlySudo   bool   // Only show sudo commands
	Cwd        string // Filter by working directory
	Since      time.Time
	Before     time.Time
}

// Stats holds history statistics.
type Stats struct {
	TotalCommands      int64
	UniqueCommands     int64
	SuccessRate        float64
	TotalAgentCalls    int64
	AgentAcceptRate    float64
	MostUsedCommands   []CommandCount
	MostFailedCommands []CommandCount
}

// CommandCount pairs a command with its usage count.
type CommandCount struct {
	Command string
	Count   int64
}
