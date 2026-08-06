package shell

import (
	"bytes"
	"io"
	"sync"
	"unicode/utf8"
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
	if len(p) >= maxStderrCapture {
		c.buf.Reset()
		_, _ = c.buf.Write(p[len(p)-maxStderrCapture:])
	} else {
		_, _ = c.buf.Write(p)
		if excess := c.buf.Len() - maxStderrCapture; excess > 0 {
			data := append([]byte(nil), c.buf.Bytes()[excess:]...)
			c.buf.Reset()
			_, _ = c.buf.Write(data)
		}
	}
	c.mu.Unlock()

	return c.original.Write(p)
}

func (c *stderrCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	data := append([]byte(nil), c.buf.Bytes()...)
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	for len(data) > 0 && !utf8.Valid(data) {
		_, size := utf8.DecodeLastRune(data)
		if size > 1 {
			break
		}
		data = data[:len(data)-1]
	}
	return string(data)
}
