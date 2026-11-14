package completion

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CobraCompleter provides completions from Cobra-based CLI tools.
// Fetches run in the background; Complete never blocks on them.
type CobraCompleter struct {
	resolvePath func(string) (string, error)
	getwd       func() string
	policy      flightPolicy // __complete results
	pathPolicy  flightPolicy // command name resolution

	results flightCache[Result] // keyed by directory, $PATH, command name and __complete args
	paths   flightCache[string] // (command name, $PATH) -> resolved path
	ready   readyNotifier

	// tools are the commands this completer may ask. Asking runs the
	// command (`<cmd> __complete <args>`), so it is never tried on a command
	// that is not known to be a Cobra tool: for rm that ran rm.
	tools map[string]bool
}

// cobraPrefetchTimeout bounds a background fetch, not the TAB the user is
// waiting on, so it can afford to be generous. A cold kubectl __complete
// takes 150-500ms; killing it early caches nothing and fails identically on
// every retry, which shows up as completion "randomly" needing several
// attempts to start working. A Tab during that time joins the running fetch.
const cobraPrefetchTimeout = 3 * time.Second

// cobraFailedPrefetchTTL is how long a failed fetch (not a cobra tool,
// timed out) is remembered, so a pending Tab's retry falls through instead
// of relaunching it.
const cobraFailedPrefetchTTL = 2 * time.Second

// knownCobraCommands are tools known to answer __complete. They are the
// only commands asked by default; the config adds more
// ([completions] cobra_commands).
var knownCobraCommands = map[string]bool{
	"kubectl":  true,
	"helm":     true,
	"gh":       true,
	"docker":   true,
	"podman":   true,
	"minikube": true,
	"kind":     true,
}

var errCobraPathUnavailable = errors.New("completion: cobra command path unavailable")

// NewCobraCompleter creates a Cobra completer that asks the known Cobra
// tools and the extra ones named.
func NewCobraCompleter(extra ...string) *CobraCompleter {
	tools := make(map[string]bool, len(knownCobraCommands)+len(extra))
	for name := range knownCobraCommands {
		tools[name] = true
	}
	for _, name := range extra {
		if name = strings.TrimSpace(name); name != "" {
			tools[name] = true
		}
	}
	c := &CobraCompleter{
		tools:       tools,
		resolvePath: exec.LookPath,
		getwd:       defaultGetwd,
		policy: flightPolicy{
			ttl:        5 * time.Minute,
			failureTTL: cobraFailedPrefetchTTL,
			timeout:    cobraPrefetchTimeout,
			wait:       0, // Complete answers from cache only
		},
		pathPolicy: flightPolicy{
			// PATH scans can block on slow mounts, so a resolved path is kept
			// for as long as PATH stays the same (re-resolved hourly).
			// Failures are not kept: the result fetch that needed the path
			// backs off instead.
			ttl:     time.Hour,
			timeout: time.Second,
			wait:    waitUntilCanceled,
		},
	}
	c.results.ready = &c.ready
	return c
}

// Name returns the completer name.
func (c *CobraCompleter) Name() string {
	return "cobra"
}

// SetOnReady registers a callback fired whenever a background fetch
// finishes, success or failure. The UI uses it to refresh a "fetching" menu
// without another TAB, or to clear it and fall back.
func (c *CobraCompleter) SetOnReady(fn func()) {
	c.ready.set(fn)
}

// cobraRequest is one __complete invocation derived from the input line.
type cobraRequest struct {
	cmdName string
	args    []string // starts with "__complete"
	// dir is where __complete runs. Many tools answer per directory (gh
	// per repository, kubectl per kubeconfig context set by direnv), so
	// it is part of the key: a result must never leak across a cd.
	dir string
	// pathEnv is $PATH when the request was made. Activating a venv or
	// entering a direnv directory changes which binary the name runs, so
	// the resolved path and its answers are keyed by it.
	pathEnv string
}

func (r cobraRequest) key() string {
	return r.dir + "\x00" + r.pathKey() + "\x00" + strings.Join(r.args, "\x00")
}

// pathKey keys the command name's resolved path.
func (r cobraRequest) pathKey() string {
	return r.cmdName + "\x00" + r.pathEnv
}

// request returns the __complete request for the line, or false when the
// cursor is not in an argument of a Cobra tool this completer may ask.
func (c *CobraCompleter) request(line string, pos int) (cobraRequest, bool) {
	req, ok := parseCobraRequest(line, pos)
	if !ok || !c.tools[req.cmdName] {
		return cobraRequest{}, false
	}
	req.dir = c.getwd()
	req.pathEnv = os.Getenv("PATH")
	return req, true
}

func parseCobraRequest(line string, pos int) (cobraRequest, bool) {
	cw := parseCommandWords(line, pos)
	// Only arguments: the command name itself is still being typed.
	if !cw.inArgumentPosition() {
		return cobraRequest{}, false
	}
	args := append([]string{"__complete"}, cw.args()...)
	args = append(args, cw.current)
	return cobraRequest{cmdName: cw.command(), args: args}, true
}

// Complete returns completions from cache only. A miss starts the
// background fetch, so a recalled or pasted line that never got a
// space-triggered prefetch still answers on the next Tab.
func (c *CobraCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	req, ok := c.request(line, pos)
	if !ok {
		return Result{}, nil
	}
	// "$VAR" and "~/..." words belong to the env and file completers; a
	// pending (or cached) cobra answer must not shadow them.
	if current := req.args[len(req.args)-1]; strings.HasPrefix(current, "$") || strings.HasPrefix(current, "~") {
		return Result{}, nil
	}

	result, status := c.results.get(ctx, req.key(), c.policy, c.fetch(req))
	switch status {
	case lookupFresh, lookupStale:
		return result, nil
	case lookupPending:
		// Only Cobra tools are asked, so a subcommand argument waits for
		// the answer instead of falling through to filenames; the ready
		// notification reopens the menu when it lands. A failed fetch
		// (lookupFailed) falls through.
		return pendingResult(), nil
	}
	return Result{}, nil
}

// Prefetch triggers background fetching of Cobra completions.
// Call this when the user types a space after a command.
func (c *CobraCompleter) Prefetch(line string, pos int) {
	req, ok := c.request(line, pos)
	if !ok {
		return
	}
	c.results.warm(req.key(), c.policy, c.fetch(req))
}

// fetch resolves the command (shared across requests for the same tool)
// and runs its __complete.
func (c *CobraCompleter) fetch(req cobraRequest) func(context.Context) (Result, error) {
	return func(ctx context.Context) (Result, error) {
		// exec.LookPath reads the process's PATH, which is pathEnv unless
		// it changed in the moment since the request.
		cmdPath, status := c.paths.get(ctx, req.pathKey(), c.pathPolicy, func(context.Context) (string, error) {
			resolve := c.resolvePath
			if resolve == nil {
				resolve = exec.LookPath
			}
			return resolve(req.cmdName)
		})
		if status != lookupFresh {
			return Result{}, errCobraPathUnavailable
		}
		return runCobraComplete(ctx, cmdPath, req.args, req.dir)
	}
}

var errNotCobraOutput = errors.New("completion: output is not a cobra __complete answer")

// runCobraComplete runs `cmdPath __complete args...` in dir and parses its
// answer.
func runCobraComplete(ctx context.Context, cmdPath string, args []string, dir string) (Result, error) {
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	// Completely isolate the process from our terminal:
	// - Setsid creates a new session, detaching from controlling terminal
	// - Stdin from /dev/null prevents reading
	// This prevents TUI apps (vim, helix) from taking over or corrupting our terminal
	configureIsolatedCompletionCommand(cmd)
	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	// A failure means the command doesn't support __complete or timed out.
	if err := cmd.Run(); err != nil {
		return Result{}, err
	}

	// A successful exit alone does not prove Cobra support: ordinary commands
	// such as echo also accept arbitrary arguments. Cobra's __complete protocol
	// always terminates stdout with a numeric directive line.
	result, valid := parseCobraOutput(stdout.String())
	if !valid {
		return Result{}, errNotCobraOutput
	}
	return result, nil
}

// cobraDirectiveNoFileComp is cobra's ShellCompDirectiveNoFileComp bit: the
// tool says files are not valid here, so an empty answer is "no matches"
// rather than a cue to fall back to filenames.
const cobraDirectiveNoFileComp = 4

// parseCobraOutput parses Cobra __complete output.
// Format: one completion per line, with an optional tab-separated description,
// followed by a final :<unsigned integer> directive line.
func parseCobraOutput(output string) (Result, bool) {
	var result Result

	lines := strings.Split(output, "\n")
	lastNonEmpty := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastNonEmpty = i
			break
		}
	}
	directive, ok := parseCobraDirective(strings.TrimSuffix(lines[max(lastNonEmpty, 0)], "\r"))
	if lastNonEmpty < 0 || !ok {
		return Result{}, false
	}

	for i, line := range lines {
		if i == lastNonEmpty {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse completion:description format
		parts := strings.SplitN(line, "\t", 2)
		value := parts[0]
		var desc string
		if len(parts) > 1 {
			desc = parts[1]
		}

		if !result.add(Item{Value: value, Display: value, Description: desc}) {
			break
		}
	}

	if directive&cobraDirectiveNoFileComp != 0 {
		result.empty = OutcomeNoMatches // owned: no matches when empty
	}
	return result, true
}

// parseCobraDirective parses the final ":<unsigned integer>" line.
func parseCobraDirective(line string) (int, bool) {
	if len(line) < 2 || line[0] != ':' {
		return 0, false
	}
	for _, r := range line[1:] {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	directive, err := strconv.Atoi(line[1:])
	return directive, err == nil
}
