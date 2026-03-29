package completion

import (
	"context"
	"strings"
)

// CommandHandler provides completions for specific commands.
type CommandHandler interface {
	// Commands returns the command names this handler supports.
	Commands() []string
	// Complete returns completions for the given command arguments.
	// args contains all arguments after the command name.
	// current is the word being completed (empty if after trailing space).
	Complete(ctx context.Context, args []string, current string) Result
}

// SemanticCompleter dispatches completions to command-specific handlers.
type SemanticCompleter struct {
	handlers map[string]CommandHandler
}

// NewSemanticCompleter creates a semantic completer with all built-in handlers.
func NewSemanticCompleter() *SemanticCompleter {
	c := &SemanticCompleter{
		handlers: make(map[string]CommandHandler),
	}

	// Register built-in handlers
	handlers := []CommandHandler{
		NewSSHHandler(),
		NewKillHandler("kill"),
		NewKillHandler("killall"),
		NewMakeHandler(),
		NewManHandler(),
		NewNPMHandler(),
		NewPipHandler("pip"),
		NewPipHandler("pip3"),
		NewBrewHandler(),
	}
	for _, h := range handlers {
		for _, cmd := range h.Commands() {
			c.handlers[cmd] = h
		}
	}

	return c
}

// Name returns the completer name.
func (c *SemanticCompleter) Name() string {
	return "semantic"
}

// Complete returns semantic completions for the current input.
func (c *SemanticCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	pipeLine, pipePos := ExtractPipeContext(line, pos)
	segment := pipeLine[:pipePos]
	trailingSpace := strings.HasSuffix(segment, " ")
	parts := strings.Fields(segment)
	if len(parts) == 0 {
		return Result{}, nil
	}

	// Only complete arguments, not command names
	if len(parts) < 2 && !trailingSpace {
		return Result{}, nil
	}

	cmdName := parts[0]
	handler, ok := c.handlers[cmdName]
	if !ok {
		return Result{}, nil
	}

	current, args := splitCurrentArg(parts[1:], trailingSpace)
	return handler.Complete(ctx, args, current), nil
}
