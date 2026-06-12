# Hash E2E Test Suite

End-to-end tests verifying the promises made on the Hash website.

## Test Types

### Mock Tests (`-tags=e2e`)

Deterministic tests using mock agents. Fast, reliable, and run in CI.

```bash
go test -tags=e2e ./e2e/...
```

**Use for:** Regression testing, CI pipelines, verifying integration logic.

### Live Tests (`-tags=e2e_live`)

Tests against the real `claude-agent-acp` agent. Non-deterministic but validates actual ACP protocol behavior.

```bash
go test -tags=e2e_live ./e2e/...
```

**Use for:** Validating ACP reliability, debugging agent integration issues, testing protocol changes.

**Requirements:** `claude-agent-acp` must be installed and accessible in PATH.

**Note:** Live tests are slower (2-3 min total) and require API quota. Agent responses vary between runs.

## Structure

```
e2e/
├── README.md               # This file
├── testdata/               # Sample files for testing
│   ├── data.csv            # Sample CSV for data processing tests
│   ├── access.log          # Sample log file for parsing tests
│   └── git-diff.patch      # Sample git diff for commit message tests
├── mock_agent.go           # Configurable mock agent with chaos testing
├── agent_syntax_test.go    # Tests for ?? prefix, pipe, inline patterns
├── data_processing_test.go # Tests for CSV/JSON transforms, log parsing
├── devops_recipes_test.go  # Tests for kubectl/docker workflows
├── git_recipes_test.go     # Tests for conventional commits, branch cleanup
├── learning_e2e_test.go    # Tests for error pattern learning
├── completion_test.go      # Tests for three-tier completion
├── live_agent_test.go      # Live agent tests (e2e_live tag)
└── debug_pipe_test.go      # Debug/diagnostic tests for pipe behavior
```

## Design Principles

1. **Deterministic** (mock): Tests use mock agents with predictable responses
2. **Isolated**: Each test has its own temp directory and config
3. **Comprehensive**: Cover all recipes/promises from the website
4. **Fast** (mock): Mock agents respond immediately, no real AI latency
5. **Chaos Testing**: Mock supports fault injection, delays, and timeouts

## Running Tests

```bash
# Run all mock e2e tests (fast, deterministic)
go test -tags=e2e ./e2e/...

# Run all live agent tests (slow, requires claude-agent-acp)
go test -tags=e2e_live ./e2e/...

# Run specific scenario
go test -tags=e2e -run TestAgentSyntax ./e2e/...

# Run specific live test with verbose output
go test -tags=e2e_live -v -run TestLive_PipeCommand ./e2e/...

# Run with timeout (live tests may take longer)
go test -tags=e2e_live -timeout 300s ./e2e/...
```

## Mock Agent Features

The `ScenarioMockTransport` supports:

- **Rule-based responses**: Match prompts by substring, regex, or context
- **Pipe detection**: Match commands with pipe output
- **Inline completion**: Match partial command lines
- **Chaos testing**:
  - `FailureRate`: Probability of random errors
  - `TimeoutRate`: Probability of simulated timeouts
  - `DisconnectRate`: Probability of connection drops
  - `MinDelay/MaxDelay`: Response latency simulation
  - `PartialResponseRate`: Truncated response simulation

Example chaos configuration:
```go
mock := NewScenarioMockWithSeed(42).
    OnPromptContains("pods", successResponse).
    WithChaos(ChaosConfig{
        FailureRate: 0.2,
        MinDelay:    10 * time.Millisecond,
        MaxDelay:    50 * time.Millisecond,
        TimeoutRate: 0.1,
    })
```

## Live Agent Tests

Live tests validate the full ACP protocol stack against `claude-agent-acp`:

| Test | Purpose |
|------|---------|
| `TestLive_FullCommand` | `?? <prompt>` syntax |
| `TestLive_PipeCommand` | `cmd \| ?? <prompt>` with clipboard context |
| `TestLive_InlineCommand` | `--flag=?? <prompt>` completion |
| `TestLive_GitDiff` | Conventional commit generation |
| `TestLive_KubectlPods` | DevOps data filtering |
| `TestLive_ContextPropagation` | Working directory context |
| `TestLive_Timeout` | Graceful timeout handling |
| `TestLive_MultipleRequests` | Session reuse across requests |

## Test Coverage Map

| Website Promise | Test File | Tag |
|-----------------|-----------|-----|
| `?? <prompt>` full command | agent_syntax_test.go | e2e |
| `cmd \| ?? <prompt>` pipe | agent_syntax_test.go | e2e |
| `--flag=?? <prompt>` inline | agent_syntax_test.go | e2e |
| Agent chaos resilience | agent_syntax_test.go | e2e |
| Context timeout handling | agent_syntax_test.go | e2e |
| CSV to JSON transform | data_processing_test.go | e2e |
| Log extraction/grouping | data_processing_test.go | e2e |
| Large output handling | data_processing_test.go | e2e |
| Kubectl health check | devops_recipes_test.go | e2e |
| Docker cleanup | devops_recipes_test.go | e2e |
| DevOps chaos resilience | devops_recipes_test.go | e2e |
| Conventional commits | git_recipes_test.go | e2e |
| Branch cleanup | git_recipes_test.go | e2e |
| Git log formatting | git_recipes_test.go | e2e |
| Git context detection | git_recipes_test.go | e2e |
| Error pattern extraction | learning_e2e_test.go | e2e |
| Fix store/retrieve | learning_e2e_test.go | e2e |
| Score threshold (≥0.7) | learning_e2e_test.go | e2e |
| Success/failure ratio | learning_e2e_test.go | e2e |
| Recency decay | learning_e2e_test.go | e2e |
| Database persistence | learning_e2e_test.go | e2e |
| Common error patterns | learning_e2e_test.go | e2e |
| Multiple fix selection | learning_e2e_test.go | e2e |
| Filesystem completion | completion_test.go | e2e |
| Directory indicator (/) | completion_test.go | e2e |
| Tilde expansion (~/) | completion_test.go | e2e |
| Completion router | completion_test.go | e2e |
| Fuzzy matching | completion_test.go | e2e |
| Priority ordering | completion_test.go | e2e |
| ACP protocol reliability | live_agent_test.go | e2e_live |
| Session management | live_agent_test.go | e2e_live |
| Timeout recovery | live_agent_test.go | e2e_live |
