package editor

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// Input that closes is the end of the session, like Ctrl+D, not a Ctrl+C:
// the shell starts a new editor after a cancel, and a closed stdin made that
// a loop that drew prompts forever (`printf 'echo hi\n' | hash`).
func TestEditor_ClosedInputEndsWithEOF(t *testing.T) {
	for _, in := range []string{"", "half typed"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := New(Config{}, strings.NewReader(in), io.Discard).Run(ctx)
		cancel()
		if err != nil {
			t.Fatalf("input %q: Run() error = %v", in, err)
		}
		if result.Exit != ExitEOF {
			t.Errorf("input %q: Exit = %v, want ExitEOF (%v)", in, result.Exit, ExitEOF)
		}
	}
}

// failingReader gives text, then fails every read with err.
type failingReader struct {
	text string
	err  error
}

func (r *failingReader) Read(b []byte) (int, error) {
	if r.text == "" {
		return 0, r.err
	}
	n := copy(b, r.text)
	r.text = r.text[n:]
	return n, nil
}

// A read error is the editor's error, every time: it used to come on a
// channel of its own beside the keys channel the feed then closed, and the
// loop could see the close first and report it as Ctrl+D (ExitEOF).
func TestEditor_ReadErrorIsReturned(t *testing.T) {
	broken := errors.New("input broke")
	for run := range 200 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := New(Config{}, &failingReader{"abc", broken}, io.Discard).Run(ctx)
		cancel()
		if !errors.Is(err, broken) {
			t.Fatalf("run %d: Run() = %+v, %v; want the read error", run, result, err)
		}
	}
}
