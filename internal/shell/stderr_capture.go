package shell

import (
	"bytes"
	"io"
	"sync"
)

const maxStderrCapture = 10 * 1024 // 10KB

// stderrCapture wraps a writer to capture stderr while still writing to original.
type stderrCapture struct {
	original io.Writer
	buf      bytes.Buffer
	mu       sync.Mutex
}

func newStderrCapture(original io.Writer) *stderrCapture {
	return &stderrCapture{original: original}
}

func (c *stderrCapture) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	// Only capture up to maxStderrCapture
	if c.buf.Len() < maxStderrCapture {
		remaining := maxStderrCapture - c.buf.Len()
		if len(p) > remaining {
			c.buf.Write(p[:remaining])
		} else {
			c.buf.Write(p)
		}
	}
	c.mu.Unlock()

	return c.original.Write(p)
}

func (c *stderrCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
