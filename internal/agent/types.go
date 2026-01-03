package agent

import (
	"context"
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

// Transport defines the interface for agent communication.
type Transport interface {
	// Connect establishes connection to the agent.
	Connect(ctx context.Context) error

	// Send sends a request and returns a channel for streaming responses.
	Send(ctx context.Context, req Request) (<-chan Response, error)

	// Close terminates the connection.
	Close() error

	// Name returns the transport name for logging.
	Name() string
}

// Client wraps a transport with convenience methods.
type Client struct {
	transport Transport
}

// NewClient creates a new agent client with the given transport.
func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

// Ask sends a prompt to the agent and waits for the response.
func (c *Client) Ask(ctx context.Context, req Request) (Response, error) {
	respCh, err := c.transport.Send(ctx, req)
	if err != nil {
		return Response{Type: ResponseTypeError, Error: err.Error()}, err
	}

	// For now, just get the first response (blocking)
	for resp := range respCh {
		return resp, nil
	}

	return Response{Type: ResponseTypeError, Error: "no response from agent"}, nil
}

// Close closes the underlying transport.
func (c *Client) Close() error {
	return c.transport.Close()
}
