package parser

import (
	"strings"

	"github.com/tfcace/hash/internal/shlex"
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

// traceDetect emits a detect_agent event. The traced guard lives here so
// Parse pays neither the map allocation nor extra branches when tracing is
// off (the common case for every dispatched line).
func traceDetect(traced bool, pattern string, position int) {
	if !traced {
		return
	}
	trace.ParserHigh("detect_agent", map[string]any{
		"pattern":  pattern,
		"position": position,
	})
}

// traceResult emits a parse_result event carrying the same keys per command
// type that the inline emissions historically used.
func traceResult(traced bool, result ParseResult) {
	if !traced {
		return
	}
	data := map[string]any{"type": result.Type.String()}
	switch result.Type {
	case CommandTypeAgent:
		data["prompt"] = result.AgentPrompt
	case CommandTypeAgentPipe, CommandTypeAgentInline:
		data["command"] = result.Command
		data["prompt"] = result.AgentPrompt
	case CommandTypeRegular:
		data["command"] = result.Command
	}
	trace.ParserDetailed("parse_result", data)
}

// Parse analyzes a command line and determines how to process it.
// Trace payloads are only built when the parser subsystem is being traced:
// this runs on every dispatched line, and the map literals are otherwise
// pure allocation for a no-op Emit.
func Parse(line string) ParseResult {
	traced := trace.Enabled("parser")
	if traced {
		trace.ParserDetailed("parse_start", map[string]any{
			"input": line,
		})
	}

	line = strings.TrimSpace(line)

	if line == "" {
		result := ParseResult{Type: CommandTypeEmpty}
		traceResult(traced, result)
		return result
	}

	marker, ok := FindAgentMarker(line)
	if !ok {
		result := ParseResult{Type: CommandTypeRegular, Command: line}
		traceResult(traced, result)
		return result
	}

	result := ParseResult{Type: marker.Type, AgentPrompt: strings.TrimSpace(line[marker.End:])}
	switch marker.Type {
	case CommandTypeAgentPipe:
		result.Command = strings.TrimSpace(line[:marker.Start])
	case CommandTypeAgentInline:
		result.Command = inlineCommand(line, marker.Start)
	}
	traceDetect(traced, marker.Type.String(), marker.Start)
	traceResult(traced, result)
	return result
}

// inlineCommand is the command an inline suggestion is appended to verbatim,
// so it keeps the boundary the user typed: everything up to a glued ??
// (--flag=??, x=??), or up to the blank before ?? with that one blank kept.
// The exception is a flag's = (--flag= ??), which absorbs the blank so the
// fill becomes the flag's value; after == or an empty assignment (FOO= ??)
// the blank is a word boundary.
func inlineCommand(line string, at int) string {
	before := line[:at]
	trimmed := strings.TrimRight(before, " \t")
	if trimmed == before || isFlagAssignment(lastWord(trimmed)) {
		return trimmed
	}
	return trimmed + line[at-1:at]
}

// isFlagAssignment reports whether word is a flag awaiting its value, as in
// --format= or -o=.
func isFlagAssignment(word string) bool {
	return strings.HasPrefix(word, "-") && strings.HasSuffix(word, "=") && !strings.HasSuffix(word, "==")
}

func lastWord(s string) string {
	return s[strings.LastIndexAny(s, " \t")+1:]
}

// Marker is an unquoted ?? that hands a line to the agent.
type Marker struct {
	Type  CommandType // CommandTypeAgent, CommandTypeAgentPipe or CommandTypeAgentInline
	Start int         // byte offset of the ?? (of the | for a pipe)
	End   int         // byte offset just past the ??
}

// FindAgentMarker finds the ?? that makes line an agent request, ignoring ??
// inside quotes, after a backslash, in a comment or inside a substitution.
// Offsets are into line as given; leading blanks are skipped.
//
//   - ?? at the start of the line: CommandTypeAgent.
//   - | (not ||) followed by ??: CommandTypeAgentPipe; a command must precede it.
//   - ?? as a word of its own after = or whitespace: CommandTypeAgentInline.
//     After whitespace it also needs a prompt, so a bare ?? argument (ls ??)
//     stays a glob; --flag=?? needs none. Everything after the ?? is the
//     prompt (cp ?? /tmp/ asks with "/tmp/"), and after a command separator
//     (make; ?? why, a || ?? b) the agent fills the next command.
//
// A pipe marker anywhere wins over an earlier inline one, so `cmd ?? x | ?? y`
// pipes. Only the inline marker must be a word of its own: ?? glued to other
// characters there (really??, ls ??.go, a??b, ls ???) is a glob. At the start
// of the line and after | the ?? marks even when text follows it directly
// (??? why asks "? why", ls | ??.go asks ".go").
func FindAgentMarker(line string) (Marker, bool) {
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	if strings.HasPrefix(line[start:], "??") {
		return Marker{Type: CommandTypeAgent, Start: start, End: start + 2}, true
	}

	var inline Marker
	found := false
	var lex shlex.Lexer
	prevCode := false // line[i-1] is shell code at the top level
	for i := 0; i < len(line); i++ {
		code := lex.Step(line[i]) == shlex.Code && lex.TopLevel()
		switch {
		case !code:
		case line[i] == '|':
			if m, ok := pipeMarker(line, i, prevCode); ok {
				return m, true
			}
		case line[i] == '?' && !found:
			inline, found = inlineMarker(line, i, prevCode)
		}
		prevCode = code
	}
	return inline, found
}

// pipeMarker matches `| ??` or `|??` at i, where line[i] == '|' is code and
// prevCode reports whether line[i-1] is.
func pipeMarker(line string, i int, prevCode bool) (Marker, bool) {
	if (prevCode && line[i-1] == '|') || (i+1 < len(line) && line[i+1] == '|') {
		return Marker{}, false // ||
	}
	j := i + 1
	for j < len(line) && isBlank(line[j]) {
		j++
	}
	if !strings.HasPrefix(line[j:], "??") || strings.TrimSpace(line[:i]) == "" {
		return Marker{}, false
	}
	return Marker{Type: CommandTypeAgentPipe, Start: i, End: j + 2}, true
}

// inlineMarker matches a ?? word at i, where line[i] == '?' is code and
// prevCode reports whether line[i-1] is: an escaped blank does not end a
// word, while = is the same character escaped or not.
func inlineMarker(line string, i int, prevCode bool) (Marker, bool) {
	if i == 0 || !strings.HasPrefix(line[i:], "??") {
		return Marker{}, false
	}
	end := i + 2
	if end < len(line) && !isBlank(line[end]) {
		return Marker{}, false // ??? or ??.go
	}
	switch prev := line[i-1]; {
	case prev == '=':
		return Marker{Type: CommandTypeAgentInline, Start: i, End: end}, true
	case prevCode && isBlank(prev) && strings.TrimSpace(line[end:]) != "":
		return Marker{Type: CommandTypeAgentInline, Start: i, End: end}, true
	}
	return Marker{}, false
}

func isBlank(c byte) bool {
	return c == ' ' || c == '\t'
}
