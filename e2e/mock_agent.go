//go:build e2e

package e2e

import (
	"context"
	"errors"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

// ChaosConfig controls fault injection and chaos testing behavior.
type ChaosConfig struct {
	// FailureRate is the probability (0.0-1.0) of returning an error
	FailureRate float64
	// MinDelay is the minimum response delay
	MinDelay time.Duration
	// MaxDelay is the maximum response delay (actual delay is random between Min and Max)
	MaxDelay time.Duration
	// TimeoutRate is the probability (0.0-1.0) of simulating a timeout (context cancellation)
	TimeoutRate float64
	// DisconnectRate is the probability (0.0-1.0) of simulating a disconnect
	DisconnectRate float64
	// PartialResponseRate is the probability (0.0-1.0) of returning partial/truncated response
	PartialResponseRate float64
	// ErrorMessages are the error messages to return on failure (random selection)
	ErrorMessages []string
}

// DefaultChaosErrors are realistic error messages for chaos testing.
var DefaultChaosErrors = []string{
	"connection reset by peer",
	"context deadline exceeded",
	"unexpected EOF",
	"agent process exited unexpectedly",
	"failed to parse response",
	"rate limit exceeded",
	"model is currently overloaded",
}

// ScenarioMockTransport is a configurable mock agent for E2E tests.
// It matches request prompts against patterns and returns corresponding responses.
type ScenarioMockTransport struct {
	mu        sync.Mutex
	rules     []ResponseRule
	requests  []agent.Request
	connected bool
	fallback  agent.Response
	chaos     *ChaosConfig
	rng       *rand.Rand
}

// ResponseRule defines a pattern->response mapping.
type ResponseRule struct {
	// PromptContains matches if the prompt contains this substring
	PromptContains string
	// PromptPattern matches if the prompt matches this regex
	PromptPattern *regexp.Regexp
	// HasPipeOutput matches if LastOutput is non-empty
	HasPipeOutput bool
	// CommandContains matches if CommandLine contains this substring
	CommandContains string
	// Response to return when matched
	Response agent.Response
}

// NewScenarioMock creates a mock transport with configurable rules.
func NewScenarioMock() *ScenarioMockTransport {
	return &ScenarioMockTransport{
		fallback: agent.Response{
			Type:  agent.ResponseTypeError,
			Error: "no matching rule for request",
		},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NewScenarioMockWithSeed creates a mock with a deterministic random seed for reproducible chaos.
func NewScenarioMockWithSeed(seed int64) *ScenarioMockTransport {
	return &ScenarioMockTransport{
		fallback: agent.Response{
			Type:  agent.ResponseTypeError,
			Error: "no matching rule for request",
		},
		rng: rand.New(rand.NewSource(seed)),
	}
}

// AddRule adds a response rule.
func (m *ScenarioMockTransport) AddRule(rule ResponseRule) *ScenarioMockTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = append(m.rules, rule)
	return m
}

// OnPromptContains adds a rule that matches when prompt contains the substring.
func (m *ScenarioMockTransport) OnPromptContains(substr string, resp agent.Response) *ScenarioMockTransport {
	return m.AddRule(ResponseRule{
		PromptContains: substr,
		Response:       resp,
	})
}

// OnPipePromptContains adds a rule that matches pipe commands.
// Note: For pipe commands, the handler includes "Given the output of" in the prompt.
func (m *ScenarioMockTransport) OnPipePromptContains(substr string, resp agent.Response) *ScenarioMockTransport {
	return m.AddRule(ResponseRule{
		PromptContains: substr,
		Response:       resp,
	})
}

// OnInlineContains adds a rule for inline completions.
func (m *ScenarioMockTransport) OnInlineContains(cmdSubstr, promptSubstr string, resp agent.Response) *ScenarioMockTransport {
	return m.AddRule(ResponseRule{
		CommandContains: cmdSubstr,
		PromptContains:  promptSubstr,
		Response:        resp,
	})
}

// SetFallback sets the response when no rules match.
func (m *ScenarioMockTransport) SetFallback(resp agent.Response) *ScenarioMockTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallback = resp
	return m
}

// WithChaos enables chaos testing with the given configuration.
func (m *ScenarioMockTransport) WithChaos(config ChaosConfig) *ScenarioMockTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(config.ErrorMessages) == 0 {
		config.ErrorMessages = DefaultChaosErrors
	}
	m.chaos = &config
	return m
}

// WithDelay adds a fixed delay to all responses.
func (m *ScenarioMockTransport) WithDelay(delay time.Duration) *ScenarioMockTransport {
	return m.WithChaos(ChaosConfig{
		MinDelay: delay,
		MaxDelay: delay,
	})
}

// WithRandomDelay adds random delay between min and max to all responses.
func (m *ScenarioMockTransport) WithRandomDelay(min, max time.Duration) *ScenarioMockTransport {
	return m.WithChaos(ChaosConfig{
		MinDelay: min,
		MaxDelay: max,
	})
}

// WithFailureRate sets the probability of random failures.
func (m *ScenarioMockTransport) WithFailureRate(rate float64) *ScenarioMockTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chaos == nil {
		m.chaos = &ChaosConfig{ErrorMessages: DefaultChaosErrors}
	}
	m.chaos.FailureRate = rate
	return m
}

// WithTimeoutRate sets the probability of simulated timeouts.
func (m *ScenarioMockTransport) WithTimeoutRate(rate float64) *ScenarioMockTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chaos == nil {
		m.chaos = &ChaosConfig{}
	}
	m.chaos.TimeoutRate = rate
	return m
}

// Name returns the transport name.
func (m *ScenarioMockTransport) Name() string {
	return "scenario-mock"
}

// Connect simulates connecting.
func (m *ScenarioMockTransport) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = true
	return nil
}

// Send finds a matching rule and returns its response.
func (m *ScenarioMockTransport) Send(ctx context.Context, req agent.Request) (<-chan agent.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	chaos := m.chaos
	rng := m.rng
	m.mu.Unlock()

	// Apply chaos: disconnect simulation
	if chaos != nil && chaos.DisconnectRate > 0 && rng.Float64() < chaos.DisconnectRate {
		return nil, errors.New("agent disconnected unexpectedly")
	}

	// Apply chaos: delay
	if chaos != nil && chaos.MaxDelay > 0 {
		delay := chaos.MinDelay
		if chaos.MaxDelay > chaos.MinDelay {
			delta := chaos.MaxDelay - chaos.MinDelay
			delay = chaos.MinDelay + time.Duration(rng.Int63n(int64(delta)))
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Apply chaos: timeout simulation
	if chaos != nil && chaos.TimeoutRate > 0 && rng.Float64() < chaos.TimeoutRate {
		return nil, context.DeadlineExceeded
	}

	respCh := make(chan agent.Response, 1)

	// Apply chaos: random failure
	if chaos != nil && chaos.FailureRate > 0 && rng.Float64() < chaos.FailureRate {
		errMsg := chaos.ErrorMessages[rng.Intn(len(chaos.ErrorMessages))]
		respCh <- agent.Response{
			Type:  agent.ResponseTypeError,
			Error: errMsg,
		}
		close(respCh)
		return respCh, nil
	}

	// Find matching rule
	m.mu.Lock()
	var matched *ResponseRule
	for i := range m.rules {
		rule := &m.rules[i]
		if m.matchesRule(rule, req) {
			matched = rule
			break
		}
	}
	fallback := m.fallback
	m.mu.Unlock()

	var resp agent.Response
	if matched != nil {
		resp = matched.Response
	} else {
		resp = fallback
	}

	// Apply chaos: partial response
	if chaos != nil && chaos.PartialResponseRate > 0 && rng.Float64() < chaos.PartialResponseRate {
		if resp.Command != "" && len(resp.Command) > 5 {
			resp.Command = resp.Command[:len(resp.Command)/2] + "..."
		}
		if resp.Explanation != "" && len(resp.Explanation) > 10 {
			resp.Explanation = resp.Explanation[:len(resp.Explanation)/2] + "..."
		}
	}

	respCh <- resp
	close(respCh)

	return respCh, nil
}

// SendStreaming adapts the scenario response API to the agent.Transport
// streaming interface used by the shell.
//
//nolint:gocritic // unnamedResult: interface parity with Transport
func (m *ScenarioMockTransport) SendStreaming(ctx context.Context, req agent.Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 1)
	errCh := make(chan error, 1)

	respCh, err := m.Send(ctx, req)
	if err != nil {
		defer close(textCh)
		errCh <- err
		close(errCh)
		return textCh, errCh
	}

	go func() {
		defer close(textCh)
		defer close(errCh)

		for resp := range respCh {
			switch resp.Type {
			case agent.ResponseTypeError:
				if resp.Error == "" {
					errCh <- errors.New("mock agent error")
				} else {
					errCh <- errors.New(resp.Error)
				}
				return
			case agent.ResponseTypeCommand:
				if resp.Command != "" {
					textCh <- resp.Command
				}
			case agent.ResponseTypeExplanation:
				if resp.Explanation != "" {
					textCh <- resp.Explanation
				}
			}
		}
	}()

	return textCh, errCh
}

func (m *ScenarioMockTransport) matchesRule(rule *ResponseRule, req agent.Request) bool {
	// Check prompt contains
	if rule.PromptContains != "" {
		if !strings.Contains(strings.ToLower(req.Prompt), strings.ToLower(rule.PromptContains)) {
			return false
		}
	}

	// Check prompt pattern
	if rule.PromptPattern != nil {
		if !rule.PromptPattern.MatchString(req.Prompt) {
			return false
		}
	}

	// Check pipe output requirement
	if rule.HasPipeOutput {
		if req.Context.LastOutput == "" {
			return false
		}
	}

	// Check command line contains
	if rule.CommandContains != "" {
		if !strings.Contains(req.CommandLine, rule.CommandContains) {
			return false
		}
	}

	return true
}

// Close simulates closing.
func (m *ScenarioMockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
	return nil
}

// Requests returns all captured requests.
func (m *ScenarioMockTransport) Requests() []agent.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]agent.Request, len(m.requests))
	copy(result, m.requests)
	return result
}

// LastRequest returns the most recent request.
func (m *ScenarioMockTransport) LastRequest() (agent.Request, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return agent.Request{}, false
	}
	return m.requests[len(m.requests)-1], true
}

// Reset clears captured requests.
func (m *ScenarioMockTransport) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = nil
}

// ResetChaos clears chaos configuration.
func (m *ScenarioMockTransport) ResetChaos() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chaos = nil
}

// ChaosStats tracks chaos injection statistics.
type ChaosStats struct {
	TotalRequests    int
	Failures         int
	Timeouts         int
	Disconnects      int
	PartialResponses int
	TotalDelay       time.Duration
}

// Stats returns chaos injection statistics (useful for verification).
func (m *ScenarioMockTransport) Stats() ChaosStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ChaosStats{
		TotalRequests: len(m.requests),
	}
}
