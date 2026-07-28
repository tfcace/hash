package completion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testPluginSpec = `
[plugin]
name = "docker-test"
commands = ["docker"]

[[rules]]
subcommands = ["rm", "container rm"]
[rules.source]
exec = ["docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}"]
delimiter = "\t"
value_column = 1
description_column = 2
cache_ttl = "1s"
`

// editorDeadlineForTest mirrors shell.editorCompletionTimeout, the budget the
// editor gives the whole completion router for one TAB.
const editorDeadlineForTest = 150 * time.Millisecond

func newTestPluginCompleter(t *testing.T, specTOML string, runner pluginRunner) *PluginCompleter {
	t.Helper()
	spec, err := ParsePluginSpec([]byte(specTOML))
	if err != nil {
		t.Fatalf("ParsePluginSpec: %v", err)
	}
	c := NewPluginCompleter([]*PluginSpec{spec})
	c.runner = runner
	return c
}

func staticRunner(lines []string) pluginRunner {
	return func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		return lines, nil
	}
}

func TestParsePluginSpec_Valid(t *testing.T) {
	spec, err := ParsePluginSpec([]byte(testPluginSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Plugin.Name != "docker-test" {
		t.Errorf("name = %q, want docker-test", spec.Plugin.Name)
	}
	if len(spec.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(spec.Rules))
	}
	rule := spec.Rules[0]
	if len(rule.subTokens) != 2 {
		t.Fatalf("subTokens = %d, want 2", len(rule.subTokens))
	}
	if got := strings.Join(rule.subTokens[1], " "); got != "container rm" {
		t.Errorf("subTokens[1] = %q, want %q", got, "container rm")
	}
	if rule.timeout != defaultPluginTimeout {
		t.Errorf("timeout = %v, want default %v", rule.timeout, defaultPluginTimeout)
	}
	if rule.cacheTTL != time.Second {
		t.Errorf("cacheTTL = %v, want 1s", rule.cacheTTL)
	}
}

func TestParsePluginSpec_Invalid(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{"missing name", `
[plugin]
commands = ["x"]
[[rules]]
[rules.source]
exec = ["x"]
`},
		{"missing commands", `
[plugin]
name = "x"
[[rules]]
[rules.source]
exec = ["x"]
`},
		{"command with space", `
[plugin]
name = "x"
commands = ["docker ps"]
[[rules]]
[rules.source]
exec = ["x"]
`},
		{"no rules", `
[plugin]
name = "x"
commands = ["x"]
`},
		{"empty exec", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
[rules.source]
exec = []
`},
		{"bad duration", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
[rules.source]
exec = ["x"]
timeout = "fast"
`},
		{"negative ttl", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
[rules.source]
exec = ["x"]
cache_ttl = "-1s"
`},
		{"negative max_args", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
max_args = -1
[rules.source]
exec = ["x"]
`},
		{"empty subcommand entry", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
subcommands = [" "]
[rules.source]
exec = ["x"]
`},
		{"not toml", `{"json": true}`},
		{"misspelled rule field", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
subcomands = ["rm"]
[rules.source]
exec = ["x"]
`},
		{"forward_flags without dash", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
forward_flags = ["n"]
[rules.source]
exec = ["x"]
`},
		{"misspelled source field", `
[plugin]
name = "x"
commands = ["x"]
[[rules]]
[rules.source]
exec = ["x"]
cache_ttl_ms = "2s"
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParsePluginSpec([]byte(tt.toml)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// cache_ttl = "0s" disables reuse between completions; timeout stays strictly
// positive because a source that can never finish is useless.
func TestParsePluginSpec_ZeroCacheTTL(t *testing.T) {
	spec, err := ParsePluginSpec([]byte(`
[plugin]
name = "x"
commands = ["x"]
[[rules]]
[rules.source]
exec = ["x"]
cache_ttl = "0s"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Rules[0].cacheTTL != 0 {
		t.Errorf("cacheTTL = %v, want 0", spec.Rules[0].cacheTTL)
	}

	if _, err := ParsePluginSpec([]byte(`
[plugin]
name = "x"
commands = ["x"]
[[rules]]
[rules.source]
exec = ["x"]
timeout = "0s"
`)); err == nil {
		t.Error("timeout = 0s should be rejected")
	}
}

// With cache_ttl = 0 every completion re-queries the source, so lists that
// change when the user runs commands (docker ps) are never stale.
func TestPluginCompleter_ZeroTTLRefetchesEveryCompletion(t *testing.T) {
	spec := `
[plugin]
name = "docker-test"
commands = ["docker"]
[[rules]]
subcommands = ["rm"]
[rules.source]
exec = ["docker", "ps", "-a"]
cache_ttl = "0s"
`
	var runs atomic.Int32
	c := newTestPluginCompleter(t, spec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		runs.Add(1)
		return []string{"web"}, nil
	})
	now := time.Now()
	c.now = func() time.Time { return now }

	line := "docker rm "
	if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Past the handoff window but well inside the default 2s TTL: a zero-TTL
	// rule must query again where a defaulted rule would reuse.
	now = now.Add(pluginHandoffTTL + 500*time.Millisecond)
	if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("source ran %d times, want 2 (zero TTL must not reuse results)", got)
	}
}

// A zero-TTL slow source must still hand its result to the completion that
// was pending on it, or the notify/retry pair would fetch forever.
func TestPluginCompleter_ZeroTTLHandsOffToPendingCompletion(t *testing.T) {
	spec := `
[plugin]
name = "docker-test"
commands = ["docker"]
[[rules]]
subcommands = ["rm"]
[rules.source]
exec = ["docker", "ps", "-a"]
cache_ttl = "0s"
`
	var runs atomic.Int32
	c := newTestPluginCompleter(t, spec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		runs.Add(1)
		time.Sleep(2 * pluginSourceWait)
		return []string{"web"}, nil
	})
	ready := make(chan struct{}, 1)
	c.SetOnReady(func() { ready <- struct{}{} })

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Pending {
		t.Fatal("precondition: slow cold source should report pending")
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no ready notification")
	}

	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "web" {
		t.Fatalf("items = %+v, want the handed-off result", result.Items)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("source ran %d times, want 1 (retrigger must consume the handoff, not refetch)", got)
	}
}

func TestParsePluginSpec_DisabledNeedsNoRules(t *testing.T) {
	spec, err := ParsePluginSpec([]byte(`
[plugin]
name = "docker"
commands = ["docker"]
disabled = true
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.Plugin.Disabled {
		t.Error("expected Disabled = true")
	}
}

func TestPluginCompleter_Complete(t *testing.T) {
	runner := staticRunner([]string{
		"abc123\tweb-server",
		"def456\tpostgres",
	})
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "abc123" || result.Items[0].Description != "web-server" {
		t.Errorf("item[0] = %+v, want value abc123 desc web-server", result.Items[0])
	}
}

func TestPluginCompleter_MultiTokenSubcommand(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))

	line := "docker container rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
}

func TestPluginCompleter_PrefixFilter(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{
		"abc123\tweb",
		"def456\tdb",
	}))

	line := "docker rm ab"
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "abc123" {
		t.Fatalf("items = %+v, want single abc123", result.Items)
	}
}

// In fuzzy mode the router does the filtering; the plugin must not throw away
// non-prefix candidates first, or fuzzy matching can never see them.
func TestPluginCompleter_FuzzyModeKeepsNonPrefixCandidates(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{
		"web-server\t...",
		"postgres\t...",
	}))
	c.SetFuzzyMode(true)

	line := "docker rm srv"
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v, want both candidates for router-level fuzzy filtering", result.Items)
	}
}

func TestPluginCompleter_NoMatchCases(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))

	tests := []struct {
		name string
		line string
	}{
		{"command name position", "docker r"},
		{"unknown subcommand", "docker push "},
		{"unknown command", "podman rm "},
		{"completing a flag", "docker rm -"},
		{"completing a flag long", "docker rm --for"},
		// Plugins outrank the env and filesystem completers, so words in
		// their domains must be declined, not owned-and-emptied.
		{"completing an env var", "docker rm $CONT"},
		{"completing a home path", "docker rm ~/back"},
		{"empty line", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.Complete(context.Background(), tt.line, len(tt.line))
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if len(result.Items) != 0 {
				t.Fatalf("items = %+v, want none", result.Items)
			}
		})
	}
}

// Flags declared in value_flags consume the following token, so
// `docker --context remote rm ` still matches the "rm" rule instead of
// treating "remote" as the subcommand.
func TestPluginCompleter_ValueFlagsConsumeTheirArgument(t *testing.T) {
	spec := `
[plugin]
name = "docker-test"
commands = ["docker"]
value_flags = ["--context", "-H"]

[[rules]]
subcommands = ["rm"]
[rules.source]
exec = ["docker", "ps", "-a"]
`
	c := newTestPluginCompleter(t, spec, staticRunner([]string{"web"}))

	for _, line := range []string{
		"docker --context remote rm ",
		"docker -H tcp://x:2375 rm ",
		"docker --context=remote rm ",
	} {
		result, err := c.Complete(context.Background(), line, len(line))
		if err != nil {
			t.Fatalf("Complete(%q): %v", line, err)
		}
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q): items = %+v, want the rm rule to match", line, result.Items)
		}
	}

	// The word after a value flag is that flag's value, not a positional the
	// plugin knows how to complete.
	line := "docker rm --context "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete(%q): %v", line, err)
	}
	if len(result.Items) != 0 || result.Handled || result.Pending {
		t.Errorf("Complete(%q) = %+v, want silence while typing a flag value", line, result)
	}
}

// forward_flags passes declared line flags into the source argv, inserted
// right after the command (docker requires global flags before the
// subcommand), so `kubectl delete pod -n staging <TAB>` queries staging.
func TestPluginCompleter_ForwardFlagsReachTheSource(t *testing.T) {
	spec := `
[plugin]
name = "kubectl"
commands = ["kubectl"]
value_flags = ["-n", "--namespace"]

[[rules]]
subcommands = ["delete pod"]
forward_flags = ["-n", "--namespace"]
[rules.source]
exec = ["kubectl", "get", "pods", "--no-headers"]
cache_ttl = "0s"
`
	queried := make(map[string]bool)
	c := newTestPluginCompleter(t, spec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		queried[strings.Join(argv, " ")] = true
		return []string{"pod-a"}, nil
	})

	// The first two lines normalize to the same source argv, so the second
	// answers from cache; queried tracks every argv the source ever saw.
	tests := []struct {
		line string
		want string
	}{
		{"kubectl delete pod -n staging ", "kubectl -n staging get pods --no-headers"},
		{"kubectl -n staging delete pod ", "kubectl -n staging get pods --no-headers"},
		{"kubectl delete pod --namespace=staging ", "kubectl --namespace=staging get pods --no-headers"},
		{"kubectl delete pod ", "kubectl get pods --no-headers"},
	}
	for _, tt := range tests {
		result, err := c.Complete(context.Background(), tt.line, len(tt.line))
		if err != nil {
			t.Fatalf("Complete(%q): %v", tt.line, err)
		}
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q): items = %+v, want the rule to match", tt.line, result.Items)
		}
		if !queried[tt.want] {
			t.Errorf("Complete(%q): source argv %q never ran; ran: %v", tt.line, tt.want, queried)
		}
	}
}

// Undeclared flags stay out of the source argv: docker rm -f must not turn
// into docker ps -f.
func TestPluginCompleter_UndeclaredFlagsAreNotForwarded(t *testing.T) {
	spec := `
[plugin]
name = "kubectl"
commands = ["kubectl"]
value_flags = ["-n"]

[[rules]]
subcommands = ["delete pod"]
forward_flags = ["-n"]
[rules.source]
exec = ["kubectl", "get", "pods"]
`
	var gotArgv []string
	c := newTestPluginCompleter(t, spec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		gotArgv = argv
		return []string{"pod-a"}, nil
	})

	line := "kubectl delete pod --force "
	if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := strings.Join(gotArgv, " "); got != "kubectl get pods" {
		t.Errorf("source argv = %q, want no forwarded flags", got)
	}
}

// Different forwarded values must not share a cache entry: pods in staging
// are not pods in prod.
func TestPluginCompleter_ForwardedFlagsSplitTheCache(t *testing.T) {
	spec := `
[plugin]
name = "kubectl"
commands = ["kubectl"]
value_flags = ["-n"]

[[rules]]
subcommands = ["delete pod"]
forward_flags = ["-n"]
[rules.source]
exec = ["kubectl", "get", "pods"]
cache_ttl = "30s"
`
	c := newTestPluginCompleter(t, spec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		return []string{strings.Join(argv, "_")}, nil
	})

	first := "kubectl delete pod -n staging "
	r1, err := c.Complete(context.Background(), first, len(first))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	second := "kubectl delete pod -n prod "
	r2, err := c.Complete(context.Background(), second, len(second))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(r1.Items) != 1 || len(r2.Items) != 1 || r1.Items[0].Value == r2.Items[0].Value {
		t.Errorf("staging item %+v vs prod item %+v: forwarded values must not share a cache entry", r1.Items, r2.Items)
	}
}

func TestPluginCompleter_FlagsIgnoredInMatch(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))

	// -f is a flag, so the positional path is still just "rm".
	line := "docker rm -f "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
}

func TestPluginCompleter_MaxArgs(t *testing.T) {
	spec := `
[plugin]
name = "docker-test"
commands = ["docker"]

[[rules]]
subcommands = ["run"]
max_args = 1
[rules.source]
exec = ["docker", "images"]
`
	c := newTestPluginCompleter(t, spec, staticRunner([]string{"ubuntu:latest"}))

	line := "docker run "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("first positional: items = %d, want 1", len(result.Items))
	}

	// After the image is given, the rule no longer applies.
	line = "docker run ubuntu:latest "
	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("second positional: items = %+v, want none", result.Items)
	}
}

func TestPluginCompleter_PipeContext(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))

	line := "history | docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
}

func TestPluginCompleter_SourceErrorIsSilent(t *testing.T) {
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		return nil, errors.New("docker daemon not running")
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete should swallow source errors, got: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none", result.Items)
	}
}

func TestPluginCompleter_CachesSourceOutput(t *testing.T) {
	var calls atomic.Int64
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		return []string{"abc123\tweb"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)
	clock := newTestClock(c)

	line := "docker rm "
	for i := 0; i < 3; i++ {
		if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner calls = %d, want 1 (cached)", got)
	}

	// Expired cache runs the source again, in the background.
	clock.advance(time.Minute)
	if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	waitForCalls(t, &calls, 2)
}

// waitForCalls waits for a background source refresh to reach want.
func waitForCalls(t *testing.T, calls *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := calls.Load(); got >= want {
			if got != want {
				t.Fatalf("runner calls = %d, want %d", got, want)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runner calls = %d, want %d", calls.Load(), want)
}

// A matched rule whose source answered still owns the argument even when no
// candidate survives the filter, so the router doesn't offer filenames for
// something like `docker rm nonexistent<TAB>`.
func TestPluginCompleter_MatchedRuleWithZeroCandidatesIsHandled(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))

	line := "docker rm zzz"
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none", result.Items)
	}
	if !result.Handled {
		t.Error("a matched rule with a successful source must report Handled")
	}
}

// A cached result from one directory must not answer for another: sources
// like `terraform workspace list` are directory-sensitive.
func TestPluginCompleter_CacheDoesNotLeakAcrossDirectories(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		return []string{filepath.Base(dir) + "-candidate"}, nil
	})
	cwd := "/projects/alpha"
	c.getwd = func() string { return cwd }

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "alpha-candidate" {
		t.Fatalf("items = %+v, want alpha-candidate", result.Items)
	}

	cwd = "/projects/beta"
	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "beta-candidate" {
		t.Fatalf("after cd: items = %+v, want beta-candidate (stale directory leak?)", result.Items)
	}
}

func TestPluginCompleter_DeduplicatesValues(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{
		"abc123\tweb",
		"abc123\tweb-duplicate",
		"",
	}))

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want single deduped item", result.Items)
	}
}

func TestPluginCompleter_WhitespaceDelimiterDefault(t *testing.T) {
	spec := `
[plugin]
name = "svc"
commands = ["svc"]

[[rules]]
subcommands = ["stop"]
[rules.source]
exec = ["svc", "list"]
description_column = 2
`
	c := newTestPluginCompleter(t, spec, staticRunner([]string{"  api   running  "}))

	line := "svc stop "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "api" || result.Items[0].Description != "running" {
		t.Fatalf("items = %+v, want api/running", result.Items)
	}
}

func TestPluginCompleter_EmptySubcommandsMatchAnyArg(t *testing.T) {
	spec := `
[plugin]
name = "svc"
commands = ["svc"]

[[rules]]
[rules.source]
exec = ["svc", "list"]
`
	c := newTestPluginCompleter(t, spec, staticRunner([]string{"api"}))

	for _, line := range []string{"svc ", "svc restart "} {
		result, err := c.Complete(context.Background(), line, len(line))
		if err != nil {
			t.Fatalf("Complete(%q): %v", line, err)
		}
		if len(result.Items) != 1 {
			t.Fatalf("Complete(%q): items = %d, want 1", line, len(result.Items))
		}
	}
}

func TestNewPluginCompleter_BuiltinDocker(t *testing.T) {
	var gotArgv []string
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		gotArgv = argv
		return []string{"web-server\tabc123  nginx:latest  (Up 2 hours)"}, nil
	}
	c := NewPluginCompleter(nil)
	c.runner = runner

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Value != "web-server" {
		t.Errorf("value = %q, want web-server", item.Value)
	}
	if !strings.Contains(item.Description, "abc123") {
		t.Errorf("description = %q, want container ID included", item.Description)
	}
	if len(gotArgv) == 0 || gotArgv[0] != "docker" || gotArgv[1] != "ps" {
		t.Errorf("argv = %v, want docker ps ...", gotArgv)
	}
	// rm completes containers in any state.
	if !containsArg(gotArgv, "-a") {
		t.Errorf("argv = %v, want -a for docker rm", gotArgv)
	}

	// Running-only subcommands must not pass -a.
	gotArgv = nil
	c2 := NewPluginCompleter(nil)
	c2.runner = runner
	line = "docker stop "
	if _, err := c2.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if containsArg(gotArgv, "-a") {
		t.Errorf("argv = %v, docker stop should list running containers only", gotArgv)
	}
}

// dockerSpecArgv returns the source argv the built-in docker spec would run
// for the given line, or nil when no rule matches.
func dockerSpecArgv(t *testing.T, line string) []string {
	t.Helper()
	var gotArgv []string
	c := NewPluginCompleter(nil)
	c.runner = func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		gotArgv = argv
		return nil, nil
	}
	if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete(%q): %v", line, err)
	}
	return gotArgv
}

// docker network connect/disconnect take NETWORK first, then CONTAINER.
func TestBuiltinDocker_NetworkSecondArgIsContainer(t *testing.T) {
	for _, sub := range []string{"connect", "disconnect"} {
		first := dockerSpecArgv(t, "docker network "+sub+" ")
		if len(first) < 3 || first[1] != "network" || first[2] != "ls" {
			t.Errorf("%s first positional: argv = %v, want docker network ls", sub, first)
		}

		second := dockerSpecArgv(t, "docker network "+sub+" mynet ")
		if len(second) < 2 || second[1] != "ps" {
			t.Errorf("%s second positional: argv = %v, want docker ps (containers)", sub, second)
		}
	}
}

func TestBuiltinDocker_ContextCompletion(t *testing.T) {
	for _, sub := range []string{"use", "rm", "inspect", "update", "export"} {
		argv := dockerSpecArgv(t, "docker context "+sub+" ")
		if len(argv) < 3 || argv[1] != "context" || argv[2] != "ls" {
			t.Errorf("docker context %s: argv = %v, want docker context ls", sub, argv)
		}
	}
}

// docker context show takes no arguments, so no rule may claim it.
func TestBuiltinDocker_ContextShowHasNoRule(t *testing.T) {
	if argv := dockerSpecArgv(t, "docker context show "); len(argv) != 0 {
		t.Errorf("docker context show: argv = %v, want no matching rule", argv)
	}
}

// The builtin spec forwards --context so completion queries the daemon the
// user is addressing, not the default one.
func TestBuiltinDocker_ForwardsContextToSource(t *testing.T) {
	argv := dockerSpecArgv(t, "docker --context remote rm ")
	if len(argv) < 4 || argv[1] != "--context" || argv[2] != "remote" || argv[3] != "ps" {
		t.Errorf("argv = %v, want docker --context remote ps ...", argv)
	}
}

// docker cp mixes container:path and local path arguments; docker's own
// completion defers to files there, and a rule would block path completion.
func TestBuiltinDocker_CpHasNoRule(t *testing.T) {
	for _, line := range []string{"docker cp ", "docker container cp "} {
		if argv := dockerSpecArgv(t, line); len(argv) != 0 {
			t.Errorf("%s: argv = %v, want no matching rule so paths complete", line, argv)
		}
	}
}

// A matched plugin rule must outrank tool-native (Cobra) completion: plugin
// output is curated and described, while __complete returns bare names, and
// which one answered must not depend on whose prefetch cache is warm.
func TestPriorities_PluginOutranksToolNative(t *testing.T) {
	if PriorityPlugin >= PriorityToolNative {
		t.Errorf("PriorityPlugin = %d, want lower (higher priority) than PriorityToolNative = %d",
			PriorityPlugin, PriorityToolNative)
	}
}

func TestBuiltinDocker_PluginCompletion(t *testing.T) {
	for _, sub := range []string{"rm", "enable", "disable", "inspect", "push", "set", "upgrade"} {
		argv := dockerSpecArgv(t, "docker plugin "+sub+" ")
		if len(argv) < 3 || argv[1] != "plugin" || argv[2] != "ls" {
			t.Errorf("docker plugin %s: argv = %v, want docker plugin ls", sub, argv)
		}
	}
}

func TestBuiltinDocker_BuilderCompletion(t *testing.T) {
	for _, sub := range []string{"use", "rm", "inspect", "stop"} {
		argv := dockerSpecArgv(t, "docker builder "+sub+" ")
		if len(argv) < 3 || argv[1] != "builder" || argv[2] != "ls" {
			t.Errorf("docker builder %s: argv = %v, want docker builder ls", sub, argv)
		}
	}
}

// docker restart works on stopped containers too, so it must not be limited
// to the running set.
func TestBuiltinDocker_RestartListsAllContainers(t *testing.T) {
	for _, line := range []string{"docker restart ", "docker container restart "} {
		argv := dockerSpecArgv(t, line)
		if len(argv) < 2 || argv[1] != "ps" {
			t.Fatalf("%q: argv = %v, want docker ps", line, argv)
		}
		if !containsArg(argv, "-a") {
			t.Errorf("%q: argv = %v, want -a (restart accepts stopped containers)", line, argv)
		}
	}
}

func TestNewPluginCompleter_BuiltinDockerRunImages(t *testing.T) {
	var gotArgv []string
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		gotArgv = argv
		return []string{"nginx:latest\tabc123  187MB"}, nil
	}
	c := NewPluginCompleter(nil)
	c.runner = runner

	line := "docker run "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "nginx:latest" {
		t.Fatalf("items = %+v, want nginx:latest", result.Items)
	}
	if !containsArg(gotArgv, "images") {
		t.Errorf("argv = %v, want docker images", gotArgv)
	}
}

func TestNewPluginCompleter_UserSpecOverridesBuiltin(t *testing.T) {
	spec, err := ParsePluginSpec([]byte(`
[plugin]
name = "my-docker"
commands = ["docker"]

[[rules]]
subcommands = ["rm"]
[rules.source]
exec = ["my-docker-helper"]
`))
	if err != nil {
		t.Fatalf("ParsePluginSpec: %v", err)
	}

	var gotArgv []string
	c := NewPluginCompleter([]*PluginSpec{spec})
	c.runner = func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		gotArgv = argv
		return []string{"custom"}, nil
	}

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "custom" {
		t.Fatalf("items = %+v, want custom", result.Items)
	}
	if len(gotArgv) == 0 || gotArgv[0] != "my-docker-helper" {
		t.Errorf("argv = %v, want my-docker-helper (user override)", gotArgv)
	}

	// The override replaces the whole docker handler: builtin-only
	// subcommands are gone too.
	line = "docker stop "
	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none after override", result.Items)
	}
}

func TestNewPluginCompleter_DisabledSpecRemovesBuiltin(t *testing.T) {
	spec, err := ParsePluginSpec([]byte(`
[plugin]
name = "docker"
commands = ["docker"]
disabled = true
`))
	if err != nil {
		t.Fatalf("ParsePluginSpec: %v", err)
	}

	c := NewPluginCompleter([]*PluginSpec{spec})
	c.runner = staticRunner([]string{"web"})

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none when disabled", result.Items)
	}
}

func TestLoadPluginSpecs(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("kubectl.toml", `
[plugin]
name = "kubectl"
commands = ["kubectl"]

[[rules]]
subcommands = ["delete pod"]
[rules.source]
exec = ["kubectl", "get", "pods", "-o", "name"]
`)
	writeFile("broken.toml", `this is not toml [`)
	writeFile("notes.txt", `ignored`)

	specs, errs := LoadPluginSpecs(dir)
	if len(specs) != 1 || specs[0].Plugin.Name != "kubectl" {
		t.Fatalf("specs = %+v, want single kubectl spec", specs)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "broken.toml") {
		t.Fatalf("errs = %v, want single broken.toml error", errs)
	}
}

func TestLoadPluginSpecs_MissingDir(t *testing.T) {
	specs, errs := LoadPluginSpecs(filepath.Join(t.TempDir(), "does-not-exist"))
	if specs != nil || errs != nil {
		t.Fatalf("specs = %v errs = %v, want nil/nil", specs, errs)
	}
}

func TestPluginCompleter_SlowSourceSurvivesUIDeadline(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		<-release
		return []string{"abc123\tweb"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	// TAB with an expired UI deadline: no items, but the source keeps going.
	line := "docker rm "
	shortCtx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := c.Complete(shortCtx, line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none under expired deadline", result.Items)
	}

	// Second TAB joins the still-running call instead of starting a new one.
	close(release)
	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want 1 after source finished", result.Items)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner calls = %d, want 1 (shared in-flight call)", got)
	}
}

func TestPluginCompleter_SlowSourceLeavesDeadlineForFallback(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		<-release
		return []string{"abc123\tweb"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	// The editor gives the whole router 150ms. A cold plugin source must give
	// up early enough that lower-priority completers still get a turn.
	line := "docker rm "
	ctx, cancel := context.WithTimeout(context.Background(), editorDeadlineForTest)
	defer cancel()

	start := time.Now()
	result, err := c.Complete(ctx, line, len(line))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none while the source is still running", result.Items)
	}
	if elapsed >= editorDeadlineForTest {
		t.Fatalf("Complete blocked for %v, want it to give up before the %v deadline", elapsed, editorDeadlineForTest)
	}
	if ctx.Err() != nil {
		t.Fatalf("completion deadline was consumed: %v", ctx.Err())
	}
}

func TestPluginCompleter_ServesStaleWhileRefreshing(t *testing.T) {
	var calls atomic.Int64
	refreshed := make(chan struct{}, 4)
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		n := calls.Add(1)
		if n == 1 {
			return []string{"abc123\told"}, nil
		}
		defer func() { refreshed <- struct{}{} }()
		return []string{"def456\tnew"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)
	clock := newTestClock(c)

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "abc123" {
		t.Fatalf("items = %+v, want the first source result", result.Items)
	}

	// testPluginSpec sets cache_ttl = 1s; step past it but stay inside the
	// stale grace window. Background refreshes read the clock, so shifting it
	// has to be synchronized.
	clock.advance(2 * time.Second)

	// The expired entry must be served immediately rather than waiting on the
	// refresh, so TAB stays instant.
	start := time.Now()
	result, err = c.Complete(context.Background(), line, len(line))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "abc123" {
		t.Fatalf("items = %+v, want the stale entry while refreshing", result.Items)
	}
	if elapsed >= pluginSourceWait {
		t.Fatalf("stale read took %v, want an immediate answer", elapsed)
	}

	// The background refresh replaces it.
	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("stale read did not trigger a background refresh")
	}

	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "def456" {
		t.Fatalf("items = %+v, want the refreshed result", result.Items)
	}
}

// testClock replaces a completer's clock with one that can be advanced from
// the test goroutine while background refreshes read it.
type testClock struct{ offset atomic.Int64 }

func newTestClock(c *PluginCompleter) *testClock {
	clock := &testClock{}
	c.now = func() time.Time { return time.Now().Add(time.Duration(clock.offset.Load())) }
	return clock
}

func (c *testClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

func TestPluginCompleter_StaleCacheExpiresAfterGrace(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		if calls.Add(1) == 1 {
			return []string{"abc123\told"}, nil
		}
		<-release
		return nil, errors.New("docker daemon is gone")
	}
	defer close(release)
	c := newTestPluginCompleter(t, testPluginSpec, runner)
	clock := newTestClock(c)

	line := "docker rm "
	if _, err := c.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Well past cache_ttl + the stale grace: the entry must not be served.
	clock.advance(pluginStaleGrace + time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), editorDeadlineForTest)
	defer cancel()
	result, err := c.Complete(ctx, line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none once the stale grace expired", result.Items)
	}
}

func TestBuiltinDockerSpec_TimeoutsSuitForRealDaemon(t *testing.T) {
	spec := mustParsePluginSpec(builtinDockerSpec)
	for i := range spec.Rules {
		rule := &spec.Rules[i]
		if rule.timeout < time.Second {
			t.Errorf("rules[%d] timeout = %v, want at least 1s (docker ps is routinely slower than the default)", i, rule.timeout)
		}
		// Docker's own commands change these lists (stop moves a container
		// between the running and stopped rules), so nothing may be reused
		// across completions.
		if rule.cacheTTL != 0 {
			t.Errorf("rules[%d] cache_ttl = %v, want 0 so completions never show pre-command state", i, rule.cacheTTL)
		}
	}
}

func TestPluginCompleter_PrefetchWarmsCache(t *testing.T) {
	var calls atomic.Int64
	ran := make(chan struct{}, 8)
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		ran <- struct{}{}
		return []string{"abc123\tweb"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	line := "docker rm "
	c.Prefetch(line, len(line))

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("prefetch never ran the source")
	}

	// The cache fill races with the source returning; a warm cache or joining
	// the in-flight call are both fine — either way TAB gets items and the
	// source ran exactly once.
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want 1 from prefetched source", result.Items)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner calls = %d, want 1", got)
	}

	// Prefetching again while cached is a no-op.
	c.Prefetch(line, len(line))
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner calls after cached prefetch = %d, want 1", got)
	}
}

func TestPluginCompleter_PrefetchIgnoresNonMatchingInput(t *testing.T) {
	var calls atomic.Int64
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		return []string{"abc123\tweb"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	for _, line := range []string{"docker ", "docker push ", "ls ", "docker rm -"} {
		c.Prefetch(line, len(line))
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("runner calls = %d, want 0 for non-matching prefetches", got)
	}
}

func TestPluginCompleter_SetUserSpecsReload(t *testing.T) {
	c := NewPluginCompleter(nil)
	c.runner = staticRunner([]string{"api"})

	// No kubectl handler initially.
	line := "kubectl delete pod "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none before reload", result.Items)
	}

	spec, err := ParsePluginSpec([]byte(`
[plugin]
name = "kubectl"
commands = ["kubectl"]

[[rules]]
subcommands = ["delete pod"]
[rules.source]
exec = ["kubectl", "get", "pods"]
`))
	if err != nil {
		t.Fatalf("ParsePluginSpec: %v", err)
	}
	c.SetUserSpecs([]*PluginSpec{spec})

	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want 1 after reload", result.Items)
	}

	// Reloading with no user specs restores built-ins only.
	c.SetUserSpecs(nil)
	result, err = c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none after clearing user specs", result.Items)
	}
}

func TestPluginCompleter_Plugins(t *testing.T) {
	spec, err := ParsePluginSpec([]byte(`
[plugin]
name = "kubectl"
commands = ["kubectl"]

[[rules]]
[rules.source]
exec = ["kubectl", "get", "pods"]
`))
	if err != nil {
		t.Fatalf("ParsePluginSpec: %v", err)
	}
	c := NewPluginCompleter([]*PluginSpec{spec})

	infos := c.Plugins()
	byCommand := make(map[string]PluginInfo, len(infos))
	for _, info := range infos {
		byCommand[info.Command] = info
	}

	docker, ok := byCommand["docker"]
	if !ok || !docker.Builtin || docker.SpecName != "docker" {
		t.Errorf("docker info = %+v, want built-in docker entry", docker)
	}
	kubectl, ok := byCommand["kubectl"]
	if !ok || kubectl.Builtin || kubectl.SpecName != "kubectl" || kubectl.Rules != 1 {
		t.Errorf("kubectl info = %+v, want user kubectl entry with 1 rule", kubectl)
	}
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

// A source that has not landed yet must be reported as pending, so callers can
// say "fetching" instead of falling through to unrelated completions.
func TestPluginCompleter_ReportsPendingWhileSourceRuns(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	runner := func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		<-release
		return []string{"abc123\tweb"}, nil
	}
	c := newTestPluginCompleter(t, testPluginSpec, runner)

	line := "docker rm "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %+v, want none while loading", result.Items)
	}
	if !result.Pending {
		t.Error("result should be marked pending while the source is still running")
	}
}

// No rule matched, so nothing is pending and other completers should run.
func TestPluginCompleter_NotPendingWhenNoRuleMatches(t *testing.T) {
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))

	line := "docker push "
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Pending {
		t.Error("no rule matched, so nothing should be pending")
	}
}

// A failing source must notify too: the editor is showing "fetching
// completions..." and needs a wake-up to clear it and fall back, or the
// notice sits there until the next keypress.
func TestPluginCompleter_NotifiesWhenSourceFails(t *testing.T) {
	ready := make(chan struct{}, 4)
	c := newTestPluginCompleter(t, testPluginSpec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		return nil, errors.New("daemon down")
	})
	c.SetOnReady(func() { ready <- struct{}{} })

	line := "docker rm "
	c.Prefetch(line, len(line))

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no ready notification after the source failed")
	}
}

// After a failure, the retriggered completion must answer immediately (not
// Pending) and must not rerun the source, or the notify/retry pair would loop.
func TestPluginCompleter_FailureIsCachedBriefly(t *testing.T) {
	var runs atomic.Int32
	c := newTestPluginCompleter(t, testPluginSpec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		runs.Add(1)
		return nil, errors.New("daemon down")
	})
	now := time.Now()
	c.now = func() time.Time { return now }

	line := "docker rm "
	rule := c.entries["docker"].rules[0]
	first := c.startSourceCall(sourceCacheKey(rule.Source.Exec, c.getwd()), rule, rule.Source.Exec, c.getwd())
	<-first.done

	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Pending {
		t.Error("a recent failure must not report Pending again")
	}
	if len(result.Items) != 0 {
		t.Errorf("items = %+v, want none", result.Items)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("source ran %d times, want 1 (failure not cached)", got)
	}

	// Once the failure entry expires, the source may run again.
	now = now.Add(pluginFailureTTL + time.Second)
	_, _ = c.Complete(context.Background(), line, len(line))
	if got := runs.Load(); got != 2 {
		t.Errorf("source ran %d times after expiry, want 2", got)
	}
}

// The completer signals when a source lands so the UI can refresh itself
// without the user pressing TAB again.
func TestPluginCompleter_NotifiesWhenSourceLands(t *testing.T) {
	ready := make(chan struct{}, 4)
	c := newTestPluginCompleter(t, testPluginSpec, staticRunner([]string{"abc123\tweb"}))
	c.SetOnReady(func() { ready <- struct{}{} })

	line := "docker rm "
	c.Prefetch(line, len(line))

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no ready notification after the source landed")
	}
}
