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
	return func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParsePluginSpec([]byte(tt.toml)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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

func TestNewPluginCompleter_BuiltinDockerRunImages(t *testing.T) {
	var gotArgv []string
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	c.runner = func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
		if rule.cacheTTL < 10*time.Second {
			t.Errorf("rules[%d] cache_ttl = %v, want at least 10s so warm TABs stay warm", i, rule.cacheTTL)
		}
	}
}

func TestPluginCompleter_PrefetchWarmsCache(t *testing.T) {
	var calls atomic.Int64
	ran := make(chan struct{}, 8)
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
	runner := func(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
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
