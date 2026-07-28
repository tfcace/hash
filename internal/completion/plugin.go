package completion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/tfcace/hash/internal/trace"
)

const (
	defaultPluginTimeout  = 500 * time.Millisecond
	defaultPluginCacheTTL = 2 * time.Second

	// pluginSourceWait bounds how long a cold TAB waits on a source. The
	// editor gives the whole router 150ms, so waiting the full budget here
	// would starve the lower-priority completers (filesystem in particular)
	// and leave TAB doing nothing at all. Give up early instead: the source
	// keeps running in the background and the next TAB is warm.
	pluginSourceWait = 40 * time.Millisecond

	// pluginStaleGrace is how far past its TTL a cached result may still be
	// served while a refresh runs in the background. Without this, any source
	// slower than pluginSourceWait is only usable in the brief window between
	// finishing and expiring.
	pluginStaleGrace = 60 * time.Second

	// pluginFailureTTL is how long a source failure is remembered. The editor
	// retries a pending completion as soon as the source finishes, so without
	// a negative cache a failing source would be relaunched by its own failure
	// notification, forever.
	pluginFailureTTL = 5 * time.Second

	// pluginHandoffTTL is how long a zero-cache_ttl source result is held.
	// It exists only to bridge the notify/retrigger gap: the completion that
	// went pending re-runs when the source lands and must find the result, or
	// it would go pending again and refetch forever. It is far shorter than
	// any realistic state change (running another docker command), so a
	// cache_ttl = 0 rule still never shows stale data in practice.
	pluginHandoffTTL = time.Second
)

// PluginSpec is a declarative completion plugin, typically loaded from a TOML
// file in <config>/completions/. Each spec attaches completion rules to one
// or more commands. A user spec whose commands overlap a built-in spec
// replaces the built-in handler for those commands.
type PluginSpec struct {
	Plugin PluginMeta   `toml:"plugin"`
	Rules  []PluginRule `toml:"rules"`
}

// PluginMeta identifies a plugin and the commands it completes.
type PluginMeta struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Commands    []string `toml:"commands"`
	// ValueFlags lists global flags that consume the next token as their
	// value (e.g. docker's --context). Without this, `docker --context remote
	// rm` would read "remote" as the subcommand and match no rule. Flags
	// given as --flag=value need no declaration.
	ValueFlags []string `toml:"value_flags"`
	// Disabled removes completions for Commands, including built-in ones.
	Disabled bool `toml:"disabled"`
}

// PluginRule matches a subcommand context and describes where completions
// come from. Rules are tried in order; the first match wins.
type PluginRule struct {
	// Subcommands lists the subcommand paths this rule applies to, e.g.
	// ["rm", "container rm"]. An empty list matches any arguments.
	Subcommands []string `toml:"subcommands"`
	// MaxArgs limits how many positional arguments after the subcommand are
	// completed by this rule (0 = unlimited). Use 1 for commands like
	// `docker run IMAGE cmd...` where only the first positional is an image.
	MaxArgs int `toml:"max_args"`
	// ForwardFlags lists flags that are copied from the command line into the
	// source argv (with their values), inserted right after the command. This
	// lets `kubectl delete pod -n staging <TAB>` query the staging namespace
	// instead of the default one. A forwarded flag whose value is a separate
	// token must also appear in the plugin's value_flags.
	ForwardFlags []string     `toml:"forward_flags"`
	Source       PluginSource `toml:"source"`

	// Compiled during validation.
	subTokens [][]string
	timeout   time.Duration
	cacheTTL  time.Duration
}

// PluginSource describes the command that produces completion candidates and
// how to parse its output. One line of output becomes one completion item.
type PluginSource struct {
	// Exec is the argv to run (no shell interpretation).
	Exec []string `toml:"exec"`
	// Delimiter splits each output line into columns ("" = whitespace).
	Delimiter string `toml:"delimiter"`
	// ValueColumn is the 1-based column inserted into the command line
	// (default 1).
	ValueColumn int `toml:"value_column"`
	// DescriptionColumn is the 1-based column shown as the item description
	// (0 = none).
	DescriptionColumn int `toml:"description_column"`
	// Timeout bounds source execution (default "500ms").
	Timeout string `toml:"timeout"`
	// CacheTTL keeps results between keystrokes (default "2s"). "0s" disables
	// reuse: every completion re-queries the source. Use it for sources whose
	// answer changes when the user runs the command itself, like docker ps.
	CacheTTL string `toml:"cache_ttl"`
}

// ParsePluginSpec parses and validates a TOML plugin spec. Unknown fields are
// errors: specs are often machine-generated, and a misspelled key silently
// falling back to its default is much harder to spot than a parse failure.
func ParsePluginSpec(data []byte) (*PluginSpec, error) {
	var spec PluginSpec
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("unknown fields:\n%s", strict.String())
		}
		return nil, err
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *PluginSpec) validate() error {
	if s.Plugin.Name == "" {
		return fmt.Errorf("plugin.name is required")
	}
	if len(s.Plugin.Commands) == 0 {
		return fmt.Errorf("plugin %q: plugin.commands must not be empty", s.Plugin.Name)
	}
	for _, cmd := range s.Plugin.Commands {
		if strings.TrimSpace(cmd) == "" || strings.ContainsAny(cmd, " \t") {
			return fmt.Errorf("plugin %q: invalid command name %q", s.Plugin.Name, cmd)
		}
	}
	for _, flag := range s.Plugin.ValueFlags {
		if !strings.HasPrefix(flag, "-") || strings.ContainsAny(flag, " \t=") {
			return fmt.Errorf("plugin %q: invalid value_flags entry %q", s.Plugin.Name, flag)
		}
	}
	if s.Plugin.Disabled {
		return nil // A disabled spec needs no rules; it only removes handlers.
	}
	if len(s.Rules) == 0 {
		return fmt.Errorf("plugin %q: at least one [[rules]] entry is required", s.Plugin.Name)
	}
	for i := range s.Rules {
		if err := s.Rules[i].validate(); err != nil {
			return fmt.Errorf("plugin %q: rules[%d]: %w", s.Plugin.Name, i, err)
		}
	}
	return nil
}

func (r *PluginRule) validate() error {
	if len(r.Source.Exec) == 0 || r.Source.Exec[0] == "" {
		return fmt.Errorf("source.exec must not be empty")
	}
	if r.MaxArgs < 0 {
		return fmt.Errorf("max_args must not be negative")
	}
	if r.Source.ValueColumn < 0 || r.Source.DescriptionColumn < 0 {
		return fmt.Errorf("column indexes are 1-based and must not be negative")
	}

	for _, flag := range r.ForwardFlags {
		if !strings.HasPrefix(flag, "-") || strings.ContainsAny(flag, " \t=") {
			return fmt.Errorf("invalid forward_flags entry %q", flag)
		}
	}

	r.subTokens = r.subTokens[:0]
	for _, sub := range r.Subcommands {
		tokens := strings.Fields(sub)
		if len(tokens) == 0 {
			return fmt.Errorf("subcommands entries must not be empty")
		}
		r.subTokens = append(r.subTokens, tokens)
	}

	var err error
	if r.timeout, err = parsePluginDuration(r.Source.Timeout, defaultPluginTimeout, false); err != nil {
		return fmt.Errorf("source.timeout: %w", err)
	}
	if r.cacheTTL, err = parsePluginDuration(r.Source.CacheTTL, defaultPluginCacheTTL, true); err != nil {
		return fmt.Errorf("source.cache_ttl: %w", err)
	}
	return nil
}

func parsePluginDuration(value string, fallback time.Duration, allowZero bool) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if d < 0 || (d == 0 && !allowZero) {
		return 0, fmt.Errorf("duration must be positive, got %q", value)
	}
	return d, nil
}

// LoadPluginSpecs loads all *.toml plugin specs from dir, sorted by filename.
// A missing directory is not an error. Files that fail to parse are skipped
// and reported in the returned error list so one broken spec cannot take
// down the others.
func LoadPluginSpecs(dir string) ([]*PluginSpec, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var specs []*PluginSpec
	var errs []error
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path) //nolint:gosec // path comes from the user's own config dir
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		spec, err := ParsePluginSpec(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		specs = append(specs, spec)
	}
	return specs, errs
}

// pluginRunner executes a completion source in dir and returns its output
// lines. The directory is passed explicitly because sources run on detached
// goroutines: by the time one starts, the shell may have cd'd elsewhere.
type pluginRunner func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error)

func defaultPluginRunner(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runIsolatedCommandIn(runCtx, dir, argv[0], argv[1:]...)
}

// PluginCompleter serves completions from declarative plugin specs.
// Built-in specs are registered first; user specs override built-ins on a
// per-command basis.
type PluginCompleter struct {
	mu      sync.RWMutex
	entries map[string]pluginEntry
	runner  pluginRunner
	cache   stringListCache
	now     func() time.Time
	getwd   func() string
	fuzzy   bool

	inflightMu sync.Mutex
	inflight   map[string]*pluginSourceCall

	failMu   sync.Mutex
	failures map[string]time.Time // source key -> when the failure entry expires

	readyMu sync.RWMutex
	onReady func()
}

// SetOnReady registers a callback fired whenever a source finishes and fills
// the cache. The UI uses it to refresh a "fetching" menu without another TAB.
func (c *PluginCompleter) SetOnReady(fn func()) {
	c.readyMu.Lock()
	c.onReady = fn
	c.readyMu.Unlock()
}

func (c *PluginCompleter) notifyReady() {
	c.readyMu.RLock()
	fn := c.onReady
	c.readyMu.RUnlock()
	if fn != nil {
		fn()
	}
}

// pluginSourceCall is a single in-flight source execution shared by all
// waiters for the same exec argv.
type pluginSourceCall struct {
	started time.Time
	done    chan struct{}
	lines   []string
	err     error
}

// pluginEntry is the handler for a single command, tracking which spec it
// came from for introspection.
type pluginEntry struct {
	specName   string
	builtin    bool
	rules      []*PluginRule
	valueFlags map[string]bool
}

// PluginInfo describes a registered plugin handler for one command.
type PluginInfo struct {
	Command  string
	SpecName string
	Builtin  bool
	Rules    int
}

// NewPluginCompleter creates a plugin completer from the built-in specs plus
// the given user specs. User specs replace built-in handlers for the commands
// they declare; a spec with disabled = true removes its commands entirely.
func NewPluginCompleter(userSpecs []*PluginSpec) *PluginCompleter {
	c := &PluginCompleter{
		runner: defaultPluginRunner,
		now:    time.Now,
		getwd:  defaultGetwd,
	}
	c.SetUserSpecs(userSpecs)
	return c
}

// SetUserSpecs replaces the user specs, rebuilding the handler table from
// built-ins plus the given specs. Safe to call while completions are running,
// so freshly written plugin files can take effect without a shell restart.
func (c *PluginCompleter) SetUserSpecs(userSpecs []*PluginSpec) {
	entries := make(map[string]pluginEntry)
	addSpec(entries, true, builtinPluginSpecs()...)
	addSpec(entries, false, userSpecs...)

	c.mu.Lock()
	c.entries = entries
	c.mu.Unlock()
}

func addSpec(entries map[string]pluginEntry, builtin bool, specs ...*PluginSpec) {
	for _, spec := range specs {
		for _, cmd := range spec.Plugin.Commands {
			if spec.Plugin.Disabled {
				delete(entries, cmd)
				continue
			}
			rules := make([]*PluginRule, len(spec.Rules))
			for i := range spec.Rules {
				rules[i] = &spec.Rules[i]
			}
			var valueFlags map[string]bool
			if len(spec.Plugin.ValueFlags) > 0 {
				valueFlags = make(map[string]bool, len(spec.Plugin.ValueFlags))
				for _, flag := range spec.Plugin.ValueFlags {
					valueFlags[flag] = true
				}
			}
			entries[cmd] = pluginEntry{
				specName:   spec.Plugin.Name,
				builtin:    builtin,
				rules:      rules,
				valueFlags: valueFlags,
			}
		}
	}
}

func (c *PluginCompleter) commandEntry(cmd string) (pluginEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[cmd]
	return entry, ok
}

// SetFuzzyMode sets whether to return all candidates (for router-level fuzzy
// filtering) instead of prefix-filtering them here.
func (c *PluginCompleter) SetFuzzyMode(enabled bool) {
	c.mu.Lock()
	c.fuzzy = enabled
	c.mu.Unlock()
}

func (c *PluginCompleter) fuzzyMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fuzzy
}

// Name returns the completer name.
func (c *PluginCompleter) Name() string {
	return "plugin"
}

// Commands returns the command names with registered plugin rules.
func (c *PluginCompleter) Commands() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.entries))
	for cmd := range c.entries {
		names = append(names, cmd)
	}
	sort.Strings(names)
	return names
}

// Plugins returns information about the registered handlers, sorted by
// command name.
func (c *PluginCompleter) Plugins() []PluginInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	infos := make([]PluginInfo, 0, len(c.entries))
	for cmd, entry := range c.entries {
		infos = append(infos, PluginInfo{
			Command:  cmd,
			SpecName: entry.specName,
			Builtin:  entry.builtin,
			Rules:    len(entry.rules),
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Command < infos[j].Command })
	return infos
}

// Complete returns plugin completions for the current input.
func (c *PluginCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	pipeLine, pipePos := ExtractPipeContext(line, pos)
	segment := pipeLine[:pipePos]
	trailingSpace := strings.HasSuffix(segment, " ")
	parts := strings.Fields(segment)
	if len(parts) == 0 {
		return Result{}, nil
	}

	// Only complete arguments, not the command name itself.
	if len(parts) < 2 && !trailingSpace {
		return Result{}, nil
	}

	entry, ok := c.commandEntry(parts[0])
	if !ok {
		return Result{}, nil
	}

	current, args := splitCurrentArg(parts[1:], trailingSpace)
	if declinesCurrentWord(current) {
		return Result{}, nil
	}

	positionals, flagGroups, currentIsFlagValue := stripFlags(args, entry.valueFlags)
	if currentIsFlagValue {
		return Result{}, nil // The word being typed belongs to a flag, not us.
	}
	dir := c.getwd()
	for _, rule := range entry.rules {
		if !rule.matches(positionals) {
			continue
		}
		argv := rule.forwardedSourceArgv(flagGroups)
		if trace.Enabled("completion") {
			trace.Emit("completion", "plugin_rule_matched", trace.LevelDetailed, map[string]any{
				"command":     parts[0],
				"subcommands": strings.Join(rule.Subcommands, ","),
				"argv":        strings.Join(argv, " "),
				"current":     current,
				"dir":         dir,
			})
		}
		items, err := c.completeRule(ctx, rule, argv, current, dir)
		if errors.Is(err, errPluginSourcePending) {
			// Matched, but the data is still on its way. Say so instead of
			// letting an unrelated completer answer for this argument.
			return Result{Pending: true}, nil
		}
		if err != nil {
			return Result{}, nil //nolint:nilerr // Source failed (tool missing, daemon down): stay silent.
		}
		// Handled even when empty: this rule owns the argument, and filenames
		// are not an answer to "which container".
		return Result{Items: items, Handled: true}, nil
	}
	return Result{}, nil
}

// declinesCurrentWord reports whether the word being completed is another
// completer's domain: flags are not ours, "$VAR" belongs to the env completer,
// and "~/..." is a path. Since a matched plugin rule owns its argument
// outright, answering these would suppress the completer the user is
// actually asking for.
func declinesCurrentWord(current string) bool {
	return strings.HasPrefix(current, "-") ||
		strings.HasPrefix(current, "$") ||
		strings.HasPrefix(current, "~")
}

// stripFlags returns the positional arguments with flags removed. A flag
// listed in valueFlags also consumes the following token as its value.
// flagGroups collects each flag occurrence with its value tokens, so a
// matched rule can forward declared flags into its source argv.
// currentIsFlagValue reports that the word being completed is the value of a
// trailing flag rather than a positional.
func stripFlags(args []string, valueFlags map[string]bool) (positionals []string, flagGroups [][]string, currentIsFlagValue bool) {
	positionals = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		if valueFlags[arg] {
			if i == len(args)-1 {
				return positionals, flagGroups, true
			}
			flagGroups = append(flagGroups, []string{arg, args[i+1]})
			i++ // Skip the flag's value.
			continue
		}
		flagGroups = append(flagGroups, []string{arg})
	}
	return positionals, flagGroups, false
}

// forwardedSourceArgv builds the source argv for a matched rule, inserting the
// line flags the rule forwards right after the command: docker requires
// global flags before the subcommand, and kubectl accepts either position.
func (r *PluginRule) forwardedSourceArgv(flagGroups [][]string) []string {
	if len(r.ForwardFlags) == 0 || len(flagGroups) == 0 {
		return r.Source.Exec
	}
	var forwarded []string
	for _, group := range flagGroups {
		name, _, _ := strings.Cut(group[0], "=")
		for _, allowed := range r.ForwardFlags {
			if name == allowed {
				forwarded = append(forwarded, group...)
				break
			}
		}
	}
	if len(forwarded) == 0 {
		return r.Source.Exec
	}
	argv := make([]string, 0, len(r.Source.Exec)+len(forwarded))
	argv = append(argv, r.Source.Exec[0])
	argv = append(argv, forwarded...)
	argv = append(argv, r.Source.Exec[1:]...)
	return argv
}

// matches reports whether the rule applies to the given positional arguments
// (subcommand path plus any completed positionals, current word excluded).
func (r *PluginRule) matches(positionals []string) bool {
	if len(r.subTokens) == 0 {
		return r.maxArgsAllows(len(positionals))
	}
	for _, tokens := range r.subTokens {
		if len(positionals) < len(tokens) {
			continue
		}
		match := true
		for i, tok := range tokens {
			if positionals[i] != tok {
				match = false
				break
			}
		}
		if match && r.maxArgsAllows(len(positionals)-len(tokens)) {
			return true
		}
	}
	return false
}

func (r *PluginRule) maxArgsAllows(completedPositionals int) bool {
	return r.MaxArgs == 0 || completedPositionals < r.MaxArgs
}

func (c *PluginCompleter) completeRule(ctx context.Context, rule *PluginRule, argv []string, current, dir string) ([]Item, error) {
	lines, err := c.sourceLines(ctx, rule, argv, dir)
	if err != nil {
		return nil, err
	}

	// In fuzzy mode all candidates go to the router, whose fuzzy filter would
	// otherwise never see the non-prefix matches it exists to find.
	prefixFilter := !c.fuzzyMode()

	var items []Item
	seen := make(map[string]bool)
	for _, line := range lines {
		value, description := rule.parseLine(line)
		if value == "" || seen[value] {
			continue
		}
		if prefixFilter && current != "" && !strings.HasPrefix(value, current) {
			continue
		}
		seen[value] = true
		items = append(items, Item{
			Value:       value,
			Display:     value,
			Description: description,
		})
		if len(items) >= completionItemLimit {
			break
		}
	}
	return items, nil
}

func (c *PluginCompleter) sourceLines(ctx context.Context, rule *PluginRule, argv []string, dir string) ([]string, error) {
	start := time.Now()
	emit := func(outcome string, lines int) {
		if !trace.Enabled("completion") {
			return
		}
		trace.Emit("completion", "plugin_lookup", trace.LevelDetailed, map[string]any{
			"argv":    strings.Join(argv, " "),
			"outcome": outcome,
			"lines":   lines,
			"wait_ms": float64(time.Since(start).Microseconds()) / 1000.0,
		})
	}

	key := sourceCacheKey(argv, dir)
	now := c.now()
	if lines, ok := c.cache.get(key, now); ok {
		emit("cache_hit", len(lines))
		return lines, nil
	}

	// A zero-TTL rule never serves expired results; freshness is the point.
	staleGrace := time.Duration(0)
	if rule.cacheTTL > 0 {
		staleGrace = pluginStaleGrace
	}

	// A source that just failed will fail again; don't relaunch it on every
	// retrigger. Old results are still worth serving while it recovers.
	if c.recentFailure(key, now) {
		if lines, ok := c.cache.getStale(key, now, staleGrace); ok {
			emit("stale_after_failure", len(lines))
			return lines, nil
		}
		emit("suppressed_after_failure", 0)
		return nil, errPluginSourceFailed
	}

	call := c.startSourceCall(key, rule, argv, dir)

	// Serve an expired-but-recent result immediately while the refresh runs.
	if lines, ok := c.cache.getStale(key, now, staleGrace); ok {
		emit("stale_while_refreshing", len(lines))
		return lines, nil
	}

	// Nothing cached: wait briefly, then leave the rest of the completion
	// deadline to the other completers. The source keeps running in the
	// background and fills the cache, so the next TAB answers instantly.
	timer := time.NewTimer(pluginSourceWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		emit("canceled", 0)
		return nil, ctx.Err()
	case <-timer.C:
		emit("pending", 0)
		return nil, errPluginSourcePending
	case <-call.done:
		if call.err != nil {
			emit("live_error", 0)
		} else {
			emit("live", len(call.lines))
		}
		return append([]string(nil), call.lines...), call.err
	}
}

// errPluginSourcePending means the source is still running. Complete treats it
// like any other source failure: stay silent and let the router move on.
var errPluginSourcePending = errors.New("completion: plugin source still running")

// errPluginSourceFailed means the source failed recently and is in the
// negative cache.
var errPluginSourceFailed = errors.New("completion: plugin source failed recently")

func (c *PluginCompleter) recentFailure(key string, now time.Time) bool {
	c.failMu.Lock()
	defer c.failMu.Unlock()
	expiry, ok := c.failures[key]
	return ok && now.Before(expiry)
}

func (c *PluginCompleter) recordFailure(key string, failed bool) {
	c.failMu.Lock()
	defer c.failMu.Unlock()
	if !failed {
		delete(c.failures, key)
		return
	}
	if c.failures == nil {
		c.failures = make(map[string]time.Time)
	}
	c.failures[key] = c.now().Add(pluginFailureTTL)
}

// sourceCacheKey includes the working directory: many sources (terraform
// workspace list, git-based tools) answer differently per directory, and a
// cached result must never leak across a cd. The argv is the final one,
// forwarded flags included, so per-namespace results stay separate too.
func sourceCacheKey(argv []string, dir string) string {
	return dir + "\x00" + strings.Join(argv, "\x00")
}

func defaultGetwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// startSourceCall returns the in-flight call for the source, starting one if
// needed. Execution is detached from the caller's context and bounded only by
// the rule's own timeout, so completion UI deadlines can't kill a slow source
// (e.g. docker ps on a cold daemon) before it ever manages to fill the cache.
func (c *PluginCompleter) startSourceCall(key string, rule *PluginRule, argv []string, dir string) *pluginSourceCall {
	now := c.now()
	c.inflightMu.Lock()
	if call, ok := c.inflight[key]; ok && !contextReadCallIsStale(call.started, now, 0) {
		c.inflightMu.Unlock()
		return call
	}
	call := &pluginSourceCall{started: now, done: make(chan struct{})}
	if c.inflight == nil {
		c.inflight = make(map[string]*pluginSourceCall)
	}
	c.inflight[key] = call
	c.inflightMu.Unlock()

	if trace.Enabled("completion") {
		trace.Emit("completion", "plugin_source_start", trace.LevelDetailed, map[string]any{
			"argv": strings.Join(argv, " "),
			"dir":  dir,
		})
	}
	go c.finishSourceCall(key, call, rule, argv, dir)
	return call
}

func (c *PluginCompleter) finishSourceCall(key string, call *pluginSourceCall, rule *PluginRule, argv []string, dir string) {
	started := time.Now()
	lines, err := c.runner(context.Background(), argv, dir, rule.timeout)
	if trace.Enabled("completion") {
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		trace.Emit("completion", "plugin_source_done", trace.LevelDetailed, map[string]any{
			"argv":        strings.Join(argv, " "),
			"dir":         dir,
			"lines":       len(lines),
			"error":       errText,
			"duration_ms": float64(time.Since(started).Microseconds()) / 1000.0,
		})
	}
	call.lines, call.err = lines, err
	if err == nil {
		ttl := rule.cacheTTL
		if ttl == 0 {
			ttl = pluginHandoffTTL // Just enough for the pending retrigger.
		}
		c.cache.set(key, lines, c.now().Add(ttl))
	}
	c.recordFailure(key, err != nil)

	c.inflightMu.Lock()
	if c.inflight[key] == call {
		delete(c.inflight, key)
	}
	c.inflightMu.Unlock()
	close(call.done)

	// Notify on failure too: the editor may be showing "fetching completions"
	// and needs a wake-up to clear it and fall back to other completers. The
	// failure entry above keeps that retry from relaunching the source.
	c.notifyReady()
}

// Prefetch warms the source cache when the input matches a plugin rule.
// The router calls this when the user types a space, giving sources a head
// start over the completion UI deadline.
func (c *PluginCompleter) Prefetch(line string, pos int) {
	pipeLine, pipePos := ExtractPipeContext(line, pos)
	segment := pipeLine[:pipePos]
	trailingSpace := strings.HasSuffix(segment, " ")
	parts := strings.Fields(segment)
	if len(parts) == 0 {
		return
	}
	if len(parts) < 2 && !trailingSpace {
		return
	}

	entry, ok := c.commandEntry(parts[0])
	if !ok {
		return
	}

	current, args := splitCurrentArg(parts[1:], trailingSpace)
	if declinesCurrentWord(current) {
		return
	}

	positionals, flagGroups, currentIsFlagValue := stripFlags(args, entry.valueFlags)
	if currentIsFlagValue {
		return
	}
	dir := c.getwd()
	for _, rule := range entry.rules {
		if !rule.matches(positionals) {
			continue
		}
		argv := rule.forwardedSourceArgv(flagGroups)
		key := sourceCacheKey(argv, dir)
		if _, cached := c.cache.get(key, c.now()); !cached {
			c.startSourceCall(key, rule, argv, dir)
		}
		return
	}
}

// parseLine splits an output line into a completion value and description.
func (r *PluginRule) parseLine(line string) (value, description string) {
	var columns []string
	if r.Source.Delimiter == "" {
		columns = strings.Fields(line)
	} else {
		columns = strings.Split(line, r.Source.Delimiter)
	}

	value = pluginColumn(columns, r.Source.ValueColumn, 1)
	description = pluginColumn(columns, r.Source.DescriptionColumn, 0)
	return strings.TrimSpace(value), strings.TrimSpace(description)
}

func pluginColumn(columns []string, index, fallback int) string {
	if index == 0 {
		index = fallback
	}
	if index <= 0 || index > len(columns) {
		return ""
	}
	return columns[index-1]
}
