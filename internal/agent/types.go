package agent

import (
	"context"
	"fmt"
)

// ResponseType indicates the kind of response from the agent.
type ResponseType int

const (
	ResponseTypeCommand     ResponseType = iota // Agent suggests a command
	ResponseTypeExplanation                     // Agent provides explanation text
	ResponseTypeError                           // Agent encountered an error
)

// Context provides context for the agent request.
type Context struct {
	Cwd         string            // Current working directory
	GitBranch   string            // Current git branch (if in repo)
	KubeContext string            // Kubernetes context (if kubectl configured)
	History     []string          // Recent command history
	EnvVars     map[string]string // Selected environment variables
	LastOutput  string            // Output from previous command (for pipe)
	LastError   string            // Error from last failed command
}

// Request represents a request to the agent.
type Request struct {
	Prompt      string  // The user's prompt
	CommandLine string  // Partial command line (for inline completion)
	Context     Context // Context for the request
}

// Response represents a response from the agent.
type Response struct {
	Type        ResponseType
	Command     string // Suggested command (if Type == ResponseTypeCommand)
	Explanation string // Explanation text (if Type == ResponseTypeExplanation)
	Error       string // Error message (if Type == ResponseTypeError)
}

// ModelOption is a model the agent advertises for selection.
type ModelOption struct {
	Value       string // identifier sent back to the agent
	Name        string // human-friendly display label
	Description string // optional longer description
}

// Transport defines the interface for agent communication.
type Transport interface {
	// Connect establishes connection to the agent.
	Connect(ctx context.Context) error

	// SendStreaming sends a request and returns channels for streaming text chunks.
	// Text chunks arrive on the first channel as they are received from the agent.
	// Errors arrive on the second channel. Both channels are closed when done.
	//
	//nolint:gocritic // unnamedResult: can't name receive-only channel returns
	SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error)

	// Close terminates the connection.
	Close() error

	// Name returns the transport name for logging.
	Name() string

	// CurrentModel returns the human-friendly name of the active model, or ""
	// when the agent exposes no model selection. Cheap, cached, never blocks.
	CurrentModel() string

	// AvailableModels returns the models the agent advertises, or nil when it
	// exposes none. Cheap, cached; call EnsureModelInfo first to populate it.
	AvailableModels() []ModelOption

	// SetModel selects a model by its value and remembers the choice for the
	// session. Returns an error if the transport or agent doesn't support it.
	SetModel(ctx context.Context, value string) error

	// EnsureModelInfo establishes whatever connection/session is needed so that
	// CurrentModel and AvailableModels report live data. No-op when not needed.
	EnsureModelInfo(ctx context.Context) error
}

// Client wraps a transport with convenience methods.
type Client struct {
	transport Transport
}

// NewClient creates a new agent client with the given transport.
func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

// Name returns the underlying transport name.
func (c *Client) Name() string {
	if c == nil || c.transport == nil {
		return ""
	}
	return c.transport.Name()
}

// Ask sends a prompt to the agent and waits for the complete response.
func (c *Client) Ask(ctx context.Context, req Request) (Response, error) {
	textCh, errCh := c.transport.SendStreaming(ctx, req)

	collector := NewStreamCollector()
	for textCh != nil || errCh != nil {
		select {
		case chunk, ok := <-textCh:
			if !ok {
				textCh = nil
				continue
			}
			collector.Append(chunk)
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				return Response{Type: ResponseTypeError, Error: err.Error()}, err
			}
		case <-ctx.Done():
			err := ctx.Err()
			return Response{Type: ResponseTypeError, Error: err.Error()}, err
		}
	}

	return collector.Response(), nil
}

// Close closes the underlying transport.
func (c *Client) Close() error {
	return c.transport.Close()
}

// CurrentModel returns the active model's display name, or "" if unavailable.
func (c *Client) CurrentModel() string {
	if c == nil || c.transport == nil {
		return ""
	}
	return c.transport.CurrentModel()
}

// AvailableModels returns the models the agent advertises, or nil.
func (c *Client) AvailableModels() []ModelOption {
	if c == nil || c.transport == nil {
		return nil
	}
	return c.transport.AvailableModels()
}

// SetModel selects a model by its value and remembers it for the session.
func (c *Client) SetModel(ctx context.Context, value string) error {
	if c == nil || c.transport == nil {
		return fmt.Errorf("no agent configured")
	}
	return c.transport.SetModel(ctx, value)
}

// EnsureModelInfo populates the transport's cached model information.
func (c *Client) EnsureModelInfo(ctx context.Context) error {
	if c == nil || c.transport == nil {
		return fmt.Errorf("no agent configured")
	}
	return c.transport.EnsureModelInfo(ctx)
}
