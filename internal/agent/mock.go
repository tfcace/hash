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

// Send returns preset responses.
func (m *MockTransport) Send(ctx context.Context, req Request) (<-chan Response, error) {
	m.requests = append(m.requests, req)

	respCh := make(chan Response, len(m.responses))
	for _, resp := range m.responses {
		respCh <- resp
	}
	close(respCh)

	return respCh, nil
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
