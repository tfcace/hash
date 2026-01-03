package parser

import (
	"strings"
)

// CommandType indicates how the input should be processed.
type CommandType int

const (
	CommandTypeEmpty       CommandType = iota // Empty input
	CommandTypeRegular                        // Normal shell command
	CommandTypeAgent                          // ?? prefix - full agent request
	CommandTypeAgentPipe                      // cmd | ?? prompt - pipe to agent
	CommandTypeAgentInline                    // cmd ?? prompt - inline agent completion
)

// ParseResult contains the parsed command components.
type ParseResult struct {
	Type        CommandType
	Command     string // The shell command part (if any)
	AgentPrompt string // The agent prompt part (if any)
}

// Parse analyzes a command line and determines how to process it.
func Parse(line string) ParseResult {
	line = strings.TrimSpace(line)

	if line == "" {
		return ParseResult{Type: CommandTypeEmpty}
	}

	// Pattern 1: ?? prefix (full agent request)
	if strings.HasPrefix(line, "??") {
		prompt := strings.TrimSpace(line[2:])
		return ParseResult{
			Type:        CommandTypeAgent,
			AgentPrompt: prompt,
		}
	}

	// Pattern 2: pipe to agent (cmd | ?? prompt)
	if idx := strings.Index(line, "| ??"); idx != -1 {
		cmd := strings.TrimSpace(line[:idx])
		prompt := strings.TrimSpace(line[idx+4:])
		return ParseResult{
			Type:        CommandTypeAgentPipe,
			Command:     cmd,
			AgentPrompt: prompt,
		}
	}

	// Also check without space: cmd |?? prompt
	if idx := strings.Index(line, "|??"); idx != -1 {
		cmd := strings.TrimSpace(line[:idx])
		prompt := strings.TrimSpace(line[idx+3:])
		return ParseResult{
			Type:        CommandTypeAgentPipe,
			Command:     cmd,
			AgentPrompt: prompt,
		}
	}

	// Pattern 3: inline agent (cmd ?? prompt) - but not at start
	if idx := strings.Index(line, " ??"); idx != -1 {
		cmd := line[:idx+1] // Include the space before ??
		// Actually, we want everything before the space-??
		cmd = strings.TrimSpace(line[:idx])
		// Check if there's an = or other connector
		if strings.HasSuffix(cmd, "=") || strings.HasSuffix(line[:idx], "=") {
			cmd = line[:idx]
		}
		prompt := strings.TrimSpace(line[idx+3:])
		return ParseResult{
			Type:        CommandTypeAgentInline,
			Command:     cmd,
			AgentPrompt: prompt,
		}
	}

	// Check for ?? without leading space (like --sort-by=??)
	if idx := strings.Index(line, "??"); idx != -1 && idx > 0 {
		cmd := line[:idx]
		prompt := strings.TrimSpace(line[idx+2:])
		return ParseResult{
			Type:        CommandTypeAgentInline,
			Command:     cmd,
			AgentPrompt: prompt,
		}
	}

	// Regular command
	return ParseResult{
		Type:    CommandTypeRegular,
		Command: line,
	}
}
