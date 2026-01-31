package parser

import (
	"strings"

	"github.com/tfcace/hash/internal/trace"
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

func (t CommandType) String() string {
	switch t {
	case CommandTypeEmpty:
		return "empty"
	case CommandTypeRegular:
		return "regular"
	case CommandTypeAgent:
		return "agent"
	case CommandTypeAgentPipe:
		return "agent_pipe"
	case CommandTypeAgentInline:
		return "agent_inline"
	default:
		return "unknown"
	}
}

// ParseResult contains the parsed command components.
type ParseResult struct {
	Type        CommandType
	Command     string // The shell command part (if any)
	AgentPrompt string // The agent prompt part (if any)
}

// makeInlineResult creates an inline agent result with tracing.
func makeInlineResult(cmd, prompt, pattern string, idx int) ParseResult {
	result := ParseResult{
		Type:        CommandTypeAgentInline,
		Command:     cmd,
		AgentPrompt: prompt,
	}
	trace.ParserHigh("detect_agent", map[string]any{
		"pattern":  pattern,
		"position": idx,
	})
	trace.ParserDetailed("parse_result", map[string]any{
		"type":    result.Type.String(),
		"command": result.Command,
		"prompt":  result.AgentPrompt,
	})
	return result
}

// Parse analyzes a command line and determines how to process it.
func Parse(line string) ParseResult {
	trace.ParserDetailed("parse_start", map[string]any{
		"input": line,
	})

	line = strings.TrimSpace(line)

	if line == "" {
		result := ParseResult{Type: CommandTypeEmpty}
		trace.ParserDetailed("parse_result", map[string]any{
			"type": result.Type.String(),
		})
		return result
	}

	// Pattern 1: ?? prefix (full agent request)
	if strings.HasPrefix(line, "??") {
		prompt := strings.TrimSpace(line[2:])
		result := ParseResult{
			Type:        CommandTypeAgent,
			AgentPrompt: prompt,
		}
		trace.ParserHigh("detect_agent", map[string]any{
			"pattern":  "prefix",
			"position": 0,
		})
		trace.ParserDetailed("parse_result", map[string]any{
			"type":   result.Type.String(),
			"prompt": result.AgentPrompt,
		})
		return result
	}

	// Pattern 2: pipe to agent (cmd | ?? prompt)
	// Only match if there's actually a command before the pipe
	if idx := strings.Index(line, "| ??"); idx > 0 {
		cmd := strings.TrimSpace(line[:idx])
		if cmd != "" {
			prompt := strings.TrimSpace(line[idx+4:])
			result := ParseResult{
				Type:        CommandTypeAgentPipe,
				Command:     cmd,
				AgentPrompt: prompt,
			}
			trace.ParserHigh("detect_agent", map[string]any{
				"pattern":  "pipe_space",
				"position": idx,
			})
			trace.ParserDetailed("parse_result", map[string]any{
				"type":    result.Type.String(),
				"command": result.Command,
				"prompt":  result.AgentPrompt,
			})
			return result
		}
	}

	// Also check without space: cmd |?? prompt
	// Only match if there's actually a command before the pipe
	if idx := strings.Index(line, "|??"); idx > 0 {
		cmd := strings.TrimSpace(line[:idx])
		if cmd != "" {
			prompt := strings.TrimSpace(line[idx+3:])
			result := ParseResult{
				Type:        CommandTypeAgentPipe,
				Command:     cmd,
				AgentPrompt: prompt,
			}
			trace.ParserHigh("detect_agent", map[string]any{
				"pattern":  "pipe_nospace",
				"position": idx,
			})
			trace.ParserDetailed("parse_result", map[string]any{
				"type":    result.Type.String(),
				"command": result.Command,
				"prompt":  result.AgentPrompt,
			})
			return result
		}
	}

	// Pattern 3: inline agent (cmd ?? prompt) - but not at start
	// Check for " ??" first, then bare "??"
	if idx := strings.Index(line, " ??"); idx != -1 {
		cmd := strings.TrimSpace(line[:idx])
		// Preserve trailing = if present (like --flag=??)
		if strings.HasSuffix(line[:idx], "=") {
			cmd = line[:idx]
		}
		prompt := strings.TrimSpace(line[idx+3:])
		return makeInlineResult(cmd, prompt, "inline_space", idx)
	}

	// Check for ?? without leading space (like --sort-by=??)
	if idx := strings.Index(line, "??"); idx > 0 {
		cmd := line[:idx]
		prompt := strings.TrimSpace(line[idx+2:])
		return makeInlineResult(cmd, prompt, "inline_nospace", idx)
	}

	// Regular command
	result := ParseResult{
		Type:    CommandTypeRegular,
		Command: line,
	}
	trace.ParserDetailed("parse_result", map[string]any{
		"type":    result.Type.String(),
		"command": result.Command,
	})
	return result
}
