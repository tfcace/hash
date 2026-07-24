package completion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	defaultPluginTimeout  = 500 * time.Millisecond
	defaultPluginCacheTTL = 2 * time.Second
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
	MaxArgs int          `toml:"max_args"`
	Source  PluginSource `toml:"source"`

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
	// CacheTTL keeps results between keystrokes (default "2s").
	CacheTTL string `toml:"cache_ttl"`
}

// ParsePluginSpec parses and validates a TOML plugin spec.
func ParsePluginSpec(data []byte) (*PluginSpec, error) {
	var spec PluginSpec
	if err := toml.Unmarshal(data, &spec); err != nil {
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

	r.subTokens = r.subTokens[:0]
	for _, sub := range r.Subcommands {
		tokens := strings.Fields(sub)
		if len(tokens) == 0 {
			return fmt.Errorf("subcommands entries must not be empty")
		}
		r.subTokens = append(r.subTokens, tokens)
	}

	var err error
	if r.timeout, err = parsePluginDuration(r.Source.Timeout, defaultPluginTimeout); err != nil {
		return fmt.Errorf("source.timeout: %w", err)
	}
	if r.cacheTTL, err = parsePluginDuration(r.Source.CacheTTL, defaultPluginCacheTTL); err != nil {
		return fmt.Errorf("source.cache_ttl: %w", err)
	}
	return nil
}

func parsePluginDuration(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
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

// pluginRunner executes a completion source and returns its output lines.
type pluginRunner func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error)

func defaultPluginRunner(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runIsolatedCommand(runCtx, argv[0], argv[1:]...)
}

// PluginCompleter serves completions from declarative plugin specs.
// Built-in specs are registered first; user specs override built-ins on a
// per-command basis.
type PluginCompleter struct {
	rules  map[string][]*PluginRule
	runner pluginRunner
	cache  stringListCache
	now    func() time.Time
}

// NewPluginCompleter creates a plugin completer from the built-in specs plus
// the given user specs. User specs replace built-in handlers for the commands
// they declare; a spec with disabled = true removes its commands entirely.
func NewPluginCompleter(userSpecs []*PluginSpec) *PluginCompleter {
	c := &PluginCompleter{
		rules:  make(map[string][]*PluginRule),
		runner: defaultPluginRunner,
		now:    time.Now,
	}
	for _, spec := range builtinPluginSpecs() {
		c.addSpec(spec)
	}
	for _, spec := range userSpecs {
		c.addSpec(spec)
	}
	return c
}

func (c *PluginCompleter) addSpec(spec *PluginSpec) {
	for _, cmd := range spec.Plugin.Commands {
		if spec.Plugin.Disabled {
			delete(c.rules, cmd)
			continue
		}
		rules := make([]*PluginRule, len(spec.Rules))
		for i := range spec.Rules {
			rules[i] = &spec.Rules[i]
		}
		c.rules[cmd] = rules
	}
}

// Name returns the completer name.
func (c *PluginCompleter) Name() string {
	return "plugin"
}

// Commands returns the command names with registered plugin rules.
func (c *PluginCompleter) Commands() []string {
	names := make([]string, 0, len(c.rules))
	for cmd := range c.rules {
		names = append(names, cmd)
	}
	sort.Strings(names)
	return names
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

	rules, ok := c.rules[parts[0]]
	if !ok {
		return Result{}, nil
	}

	current, args := splitCurrentArg(parts[1:], trailingSpace)
	if strings.HasPrefix(current, "-") {
		return Result{}, nil // Flags are not ours to complete.
	}

	positionals := nonFlagArgs(args)
	for _, rule := range rules {
		if !rule.matches(positionals) {
			continue
		}
		items, err := c.completeRule(ctx, rule, current)
		if err != nil {
			return Result{}, nil // Source failed (tool missing, daemon down): stay silent.
		}
		return Result{Items: items}, nil
	}
	return Result{}, nil
}

func nonFlagArgs(args []string) []string {
	positionals := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positionals = append(positionals, arg)
	}
	return positionals
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

func (c *PluginCompleter) completeRule(ctx context.Context, rule *PluginRule, current string) ([]Item, error) {
	lines, err := c.sourceLines(ctx, rule)
	if err != nil {
		return nil, err
	}

	var items []Item
	seen := make(map[string]bool)
	for _, line := range lines {
		value, description := rule.parseLine(line)
		if value == "" || seen[value] {
			continue
		}
		if current != "" && !strings.HasPrefix(value, current) {
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

func (c *PluginCompleter) sourceLines(ctx context.Context, rule *PluginRule) ([]string, error) {
	key := strings.Join(rule.Source.Exec, "\x00")
	now := c.now()
	if lines, ok := c.cache.get(key, now); ok {
		return lines, nil
	}

	lines, err := c.runner(ctx, rule.Source.Exec, rule.timeout)
	if err != nil {
		return nil, err
	}
	c.cache.set(key, lines, c.now().Add(rule.cacheTTL))
	return lines, nil
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
