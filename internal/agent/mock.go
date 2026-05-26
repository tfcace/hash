package agent

import (
	"context"
)

// MockTransport is a transport for testing.
type MockTransport struct {
	responses []Response
	requests  []Request
	connected bool
}

// NewMockTransport creates a new mock transport with preset responses.
func NewMockTransport(responses ...Response) *MockTransport {
	return &MockTransport{
		responses: responses,
	}
}

// Name returns the transport name.
func (m *MockTransport) Name() string {
	return "mock"
}

// Connect simulates connecting.
func (m *MockTransport) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

// SendStreaming returns preset responses as streaming text chunks.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (m *MockTransport) SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error) {
	m.requests = append(m.requests, req)

	textCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(textCh)
		defer close(errCh)

		for _, resp := range m.responses {
			if resp.Type == ResponseTypeError {
				errCh <- &AgentError{Message: resp.Error}
				return
			}
			if resp.Command != "" {
				textCh <- resp.Command
			} else if resp.Explanation != "" {
				textCh <- resp.Explanation
			}
		}
	}()

	return textCh, errCh
}

// Close simulates closing.
func (m *MockTransport) Close() error {
	m.connected = false
	return nil
}

// Requests returns the captured requests.
func (m *MockTransport) Requests() []Request {
	return m.requests
}

// Compile-time check
var _ Transport = (*MockTransport)(nil)
