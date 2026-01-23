package executor

import (
	"io"
	"sync"
)

// switchableWriter wraps an io.Writer that can be swapped between executions.
// This allows a persistent interpreter runner to have its output redirected
// per-execution while maintaining state (like function definitions) across calls.
type switchableWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSwitchableWriter(w io.Writer) *switchableWriter {
	return &switchableWriter{w: w}
}

func (s *switchableWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	w := s.w
	s.mu.Unlock()
	if w == nil {
		return len(p), nil // discard if no writer set
	}
	return w.Write(p)
}

// Set changes the underlying writer. Thread-safe.
func (s *switchableWriter) Set(w io.Writer) {
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
}
