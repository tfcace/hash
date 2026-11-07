package completion

import (
	"context"
	"time"
)

// sourceWait is how long a Tab waits on a semantic or VCS source with
// nothing cached before reporting pending. The editor gives the whole router
// 150ms; a source that lands later fills the menu in through the ready
// notification.
const sourceWait = 100 * time.Millisecond

// fileSourceTimeout bounds reading small config files (Makefile,
// package.json, ssh config).
const fileSourceTimeout = time.Second

// sourceResult turns a source lookup into a handler result: values become
// prefix-filtered items and a lookup still running claims the argument.
func sourceResult(values []string, status lookupStatus, prefix string) Result {
	switch status {
	case lookupPending:
		return pendingResult()
	case lookupFailed:
		return Result{}
	default:
		return prefixFilterItems(values, prefix)
	}
}

// readyAware is implemented by handlers whose sources run in the background
// and can report pending.
type readyAware interface {
	setReady(*readyNotifier)
}

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
	ready    readyNotifier
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
		if ra, ok := h.(readyAware); ok {
			ra.setReady(&c.ready)
		}
		for _, cmd := range h.Commands() {
			c.handlers[cmd] = h
		}
	}

	return c
}

// SetOnReady registers a callback fired whenever a background source lands,
// so a "fetching" menu can fill itself in without another Tab.
func (c *SemanticCompleter) SetOnReady(fn func()) {
	c.ready.set(fn)
}

// Name returns the completer name.
func (c *SemanticCompleter) Name() string {
	return "semantic"
}

// Complete returns semantic completions for the current input.
func (c *SemanticCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	cw := parseCommandWords(line, pos)
	// Only complete arguments, not command names or redirection targets.
	if !cw.inArgumentPosition() {
		return Result{}, nil
	}
	handler, ok := c.handlers[cw.command()]
	if !ok {
		return Result{}, nil
	}
	return handler.Complete(ctx, cw.args(), cw.current), nil
}
