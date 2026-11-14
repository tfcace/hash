package editor

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// screenBuf collects what the editor writes, safe to read while it runs.
type screenBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *screenBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *screenBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// lastFrame is what the last input render drew: from its hide-cursor to the
// end of the output.
func (s *screenBuf) lastFrame() string {
	out := s.String()
	if i := strings.LastIndex(out, ansiHideCursor); i >= 0 {
		return out[i:]
	}
	return out
}

// lateCompletion is a completer whose source lands when the test says so.
func lateCompletion(items ...Completion) (Config, func()) {
	ready := make(chan struct{}, 1)
	var landed atomic.Bool
	cfg := Config{
		CompletionReadyCh: ready,
		CompleteOutcomeFunc: func(line string, pos int) CompletionOutcome {
			if !landed.Load() {
				return EmptyOutcome(CompletionPending)
			}
			return wordMenu(line, pos, items...)
		},
	}
	return cfg, func() { landed.Store(true); ready <- struct{}{} }
}

// eventually waits for cond, failing with msg after a second.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal(msg)
}

// A Tab still fetching lands as a lone match: the line changes, and the
// screen must show it before Enter runs it. It used to change without a
// redraw, so Enter ran "git stash" while the screen showed "git st".
func TestRun_LateLoneMatchIsDrawnBeforeEnter(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	cfg, land := lateCompletion(Completion{Text: "stash"})
	out := &screenBuf{}
	ed := New(cfg, pr, out)
	ed.SetInitialText("git st")

	results := make(chan Result, 1)
	go func() {
		res, _ := ed.Run(context.Background())
		results <- res
	}()
	pw.WriteString("\t")
	eventually(t, func() bool { return strings.Contains(out.String(), "fetching completions") }, "fetching notice never drawn")
	land()
	eventually(t, func() bool { return strings.Contains(out.lastFrame(), "git stash") },
		"the lone match changed the line but the screen still shows: "+out.lastFrame())
	if strings.Contains(out.lastFrame(), "fetching") {
		t.Errorf("the fetching notice is still the last thing drawn: %q", out.lastFrame())
	}
	pw.WriteString("\r")
	if res := <-results; res.Text != "git stash" {
		t.Errorf("submitted %q", res.Text)
	}
}

// A late source with several matches opens the menu without a key.
func TestRun_LateMenuOpensWithoutAKey(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	cfg, land := lateCompletion(Completion{Text: "alphaitem"}, Completion{Text: "betaitem"})
	out := &screenBuf{}
	ed := New(cfg, pr, out)
	ed.SetInitialText("git ")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ed.Run(ctx) //nolint:errcheck // canceled at the end of the test
	pw.WriteString("\t")
	eventually(t, func() bool { return strings.Contains(out.String(), "fetching completions") }, "fetching notice never drawn")
	land()
	eventually(t, func() bool { return strings.Contains(out.String(), "alphaitem") }, "menu never drawn after the source landed")
}
