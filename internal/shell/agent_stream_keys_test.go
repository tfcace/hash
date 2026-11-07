package shell

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/editor"
)

// typedKeys is a key reader on a pipe the test types into, as the shell's
// reader reads the terminal.
func typedKeys(t *testing.T) (*editor.InputReader, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return typingSlowly(editor.NewInputReader(r)), w
}

func startTestWatcher(keys *editor.InputReader, onEsc func()) *streamKeyWatcher {
	w := newStreamKeyWatcher(keys, onEsc)
	w.start()
	return w
}

// readKeys reads n keys from r, failing after a second.
func readKeys(t *testing.T, r *editor.InputReader, n int) []editor.Key {
	t.Helper()
	got := make(chan []editor.Key, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		var keys []editor.Key
		for len(keys) < n {
			k, err := r.ReadKeyInterruptible(done)
			if err != nil {
				break
			}
			keys = append(keys, k)
		}
		got <- keys
	}()
	select {
	case keys := <-got:
		return keys
	case <-time.After(time.Second):
		t.Fatalf("fewer than %d keys to read", n)
		return nil
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStreamKeyWatcher_EscCancels(t *testing.T) {
	t.Parallel()
	keys, typed := typedKeys(t)
	var canceled atomic.Bool
	w := startTestWatcher(keys, func() { canceled.Store(true) })
	defer w.Stop()

	typed.WriteString("\x1b")
	waitFor(t, canceled.Load)
}

// Keys typed during a turn are typeahead for the next prompt: the watcher
// keeps them, in order, and gives them back when it stops. Arrows and
// Keys other than Esc are kept, in order, for the next prompt. The watcher
// used to read the terminal itself and swallow them: `?? hello⏎echo BBB2⏎`
// lost the line.
func TestStreamKeyWatcher_KeepsOtherKeysForTheNextPrompt(t *testing.T) {
	keys, typed := typedKeys(t)
	var canceled atomic.Bool
	w := startTestWatcher(keys, func() { canceled.Store(true) })

	typed.WriteString("ab\x1b[A\n")
	waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return len(w.kept) == 4 })
	w.Stop()

	if canceled.Load() {
		t.Error("a non-Esc key canceled the turn")
	}
	want := []editor.Key{{Rune: 'a'}, {Rune: 'b'}, {Special: editor.KeyUp}, {Special: editor.KeyEnter}}
	if got := readKeys(t, keys, len(want)); !slices.Equal(got, want) {
		t.Errorf("next reader got %+v, want %+v", got, want)
	}
}

// Esc, then a key typed quickly after it, is Esc, then the key: the turn
// is canceled and the key, without Alt, is the next prompt's. ESC and the
// key in one write is an Alt chord (what tmux with extended-keys off and
// Terminal.app with Option as Meta send for Alt+l): it does not cancel the
// turn and is kept as typed. It used to cancel the turn.
func TestStreamKeyWatcher_EscThenAQuickKeyCancels(t *testing.T) {
	t.Parallel()
	for _, gap := range []time.Duration{0, 20 * time.Millisecond, 40 * time.Millisecond} {
		keys, typed := typedKeys(t)
		var canceled atomic.Bool
		w := startTestWatcher(keys, func() { canceled.Store(true) })
		if gap == 0 {
			typed.WriteString("\x1bl")
		} else {
			typeQuickly(keys, typed, "\x1b", "l")
		}
		waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return len(w.kept) > 0 })
		w.Stop()
		want := editor.Key{Rune: 'l'}
		if gap == 0 {
			want.Alt = true
		}
		if canceled.Load() != (gap > 0) {
			t.Errorf("gap %v: canceled %v, want %v", gap, canceled.Load(), gap > 0)
		}
		if got := readKeys(t, keys, 1); got[0] != want {
			t.Errorf("gap %v: the next prompt gets %+v, want %+v", gap, got[0], want)
		}
	}
}

// Alt+Enter (ESC CR, what some terminals send for Shift+Enter) and keys
// whose Alt the terminal encodes in a CSI sequence (Alt+Left is ESC[1;3D)
// cannot come from typing Esc then a key: they are kept as typed, and the
// turn goes on. Every Alt key used to cancel the turn, and Shift+Enter as
// ESC CR then ran the typed-ahead line at the next prompt.
func TestStreamKeyWatcher_AltKeysThatAreNotEscThenAKeyAreKept(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		typed string
		want  []editor.Key
	}{
		{"ls\x1b\r", []editor.Key{{Rune: 'l'}, {Rune: 's'}, {Special: editor.KeyEnter, Alt: true}}},
		{"ls\x1b[1;3D", []editor.Key{{Rune: 'l'}, {Rune: 's'}, {Special: editor.KeyLeft, Alt: true}}},
		{"ls\x1b[1;3C", []editor.Key{{Rune: 'l'}, {Rune: 's'}, {Special: editor.KeyRight, Alt: true}}},
		// Alt+. as kitty's CSI u and xterm's modifyOtherKeys send it.
		{"ls\x1b[46;3u", []editor.Key{{Rune: 'l'}, {Rune: 's'}, {Rune: '.', Alt: true}}},
		{"ls\x1b[27;3;46~", []editor.Key{{Rune: 'l'}, {Rune: 's'}, {Rune: '.', Alt: true}}},
	} {
		keys, typed := typedKeys(t)
		var canceled atomic.Bool
		w := startTestWatcher(keys, func() { canceled.Store(true) })
		typed.WriteString(tc.typed)
		waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return canceled.Load() || len(w.kept) == len(tc.want) })
		w.Stop()
		if canceled.Load() {
			t.Errorf("%q canceled the turn", tc.typed)
			continue
		}
		if got := readKeys(t, keys, len(tc.want)); !slices.Equal(got, tc.want) {
			t.Errorf("%q: the next prompt gets %+v, want %+v", tc.typed, got, tc.want)
		}
	}
}

// typeQuickly writes parts to typed as quick typing: each once keys has
// taken the last off the pipe, then 10ms later, inside the reader's 50ms
// escape window. A loaded machine only stretches the pause (past the
// window, the keys are two either way), never shortens it.
func typeQuickly(keys *editor.InputReader, typed *os.File, parts ...string) {
	for i, p := range parts {
		if i > 0 {
			waitDrained(keys)
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = typed.WriteString(p)
	}
}

// waitDrained waits until keys has read every byte written to its pipe.
func waitDrained(keys *editor.InputReader) {
	fd := int32(keys.File().Fd()) //nolint:gosec // a pipe's descriptor
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		fds := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
		if n, err := unix.Poll(fds, 0); err == nil && n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// Esc then a quick Enter during a turn is Esc (cancel), then Enter, which
// ends the typed-ahead line: the next prompt runs only what came before
// it. The pair used to read as Alt+Enter: the turn went on and the next
// prompt got a two-line command, "ls" and the line typed after.
func TestStreamKeyWatcher_EscThenAQuickEnterCancels(t *testing.T) {
	keys, typed := typedKeys(t)
	var canceled atomic.Bool
	w := startTestWatcher(keys, func() { canceled.Store(true) })
	typeQuickly(keys, typed, "ls", "\x1b", "\r", "echo two\r")
	waitDrained(keys)
	time.Sleep(20 * time.Millisecond)
	w.Stop()
	if !canceled.Load() {
		t.Error("Esc then a quick Enter did not cancel the turn")
	}
	want := []editor.Key{{Rune: 'l'}, {Rune: 's'}, {Special: editor.KeyEnter}}
	if got := readKeys(t, keys, len(want)); !slices.Equal(got, want) {
		t.Errorf("the next prompt gets %+v, want %+v", got, want)
	}
	if rest := keys.Typeahead(); len(rest) > 0 {
		t.Errorf("past the first Enter the reader still holds %+v", rest)
	}
}

// A key typed while Stop waits for the watcher's read reaches the next
// reader: the watcher used to check for Stop before its poll and read the
// key after it, then swallow it.
func TestStreamKeyWatcher_KeyTypedDuringStopIsNotLost(t *testing.T) {
	t.Parallel()
	for run := range 50 {
		keys, typed := typedKeys(t)
		var canceled atomic.Bool
		w := startTestWatcher(keys, func() { canceled.Store(true) })
		time.Sleep(time.Millisecond) // the watcher is waiting for a key
		go typed.WriteString("l\x1b")
		w.Stop()
		if got := readKeys(t, keys, 2); len(got) != 2 || got[0] != (editor.Key{Rune: 'l'}) {
			t.Fatalf("run %d: next reader got %+v, want l then Esc", run, got)
		}
		_ = canceled.Load() // an Esc read before Stop may cancel; one read after it goes on
	}
}

// A permission prompt asks the watcher for its key: the next answer key
// answers it, a lone Esc included (Esc denies), instead of canceling the
// turn. Motion keys are not answers; they do nothing.
func TestStreamKeyWatcher_AskAnswersThePrompt(t *testing.T) {
	t.Parallel()
	keys, typed := typedKeys(t)
	var canceled atomic.Bool
	w := startTestWatcher(keys, func() { canceled.Store(true) })
	defer w.Stop()

	got := make(chan byte)
	ask := w.Ask(nil)
	ctx := questionCtx(t)
	go func() { got <- ask.Wait(ctx) }()
	typed.WriteString("\x1b[A")
	time.Sleep(10 * time.Millisecond)
	typed.WriteString("\x1b")
	if k := <-got; k != 0x1b {
		t.Errorf("prompt got %q, want Esc", k)
	}
	if canceled.Load() {
		t.Error("the Esc that answered the prompt also canceled the turn")
	}

	typed.WriteString("\x1b") // the prompt is gone: Esc cancels again
	waitFor(t, canceled.Load)
}

// An Alt chord sent as ESC and the key at a permission prompt is no Esc:
// Alt+y denies (it is not y), and nothing reaches the next prompt.
func TestStreamKeyWatcher_AnAltChordAtThePromptDenies(t *testing.T) {
	keys, typed := typedKeys(t)
	var canceled atomic.Bool
	w := startTestWatcher(keys, func() { canceled.Store(true) })
	ask := w.Ask(nil)
	got := make(chan byte, 1)
	ctx := questionCtx(t)
	go func() { got <- ask.Wait(ctx) }()
	typed.WriteString("\x1by")
	select {
	case k := <-got:
		if permissionAnswerForKey(k) != permissionDeny {
			t.Errorf("prompt got %q, want a deny", k)
		}
	case <-time.After(time.Second):
		t.Fatal("Alt+y did not answer the prompt")
	}
	w.Stop()
	if canceled.Load() {
		t.Error("Alt+y canceled the turn")
	}
	if rest := keys.Typeahead(); len(rest) > 0 {
		t.Errorf("the next prompt gets %+v, want nothing", rest)
	}
}

// Each key a prompt can get, as the byte permissionAnswerForKey reads.
// An Alt chord sent as ESC and the key is no Esc: Alt+y denies, as an
// encoded Alt+y does.
func TestAnswerKey(t *testing.T) {
	cases := []struct {
		key  editor.Key
		want byte
		ok   bool
	}{
		{editor.Key{Rune: 'y'}, 'y', true},
		{editor.Key{Rune: 'A'}, 'A', true},
		{editor.Key{Special: editor.KeyEnter}, '\r', true},
		{editor.Key{Special: editor.KeyEscape}, 0x1b, true},
		{editor.Key{Rune: 'c', Ctrl: true}, 0x03, true},
		{editor.Key{Rune: 'y', Ctrl: true}, 0, true},
		{editor.Key{Rune: 'y', Alt: true}, 0, true},
		{editor.Key{Rune: 'y', Alt: true}, 0, true},
		{editor.Key{Rune: 'é'}, 0, true},
		{editor.Key{Special: editor.KeyTab}, 0, false},
		{editor.Key{Special: editor.KeyUp}, 0, false},
	}
	for _, c := range cases {
		got, ok := answerKey(c.key)
		if got != c.want || ok != c.ok {
			t.Errorf("answerKey(%+v) = %#x, %v; want %#x, %v", c.key, got, ok, c.want, c.ok)
		}
	}
}

// A prompt whose request ends, or whose watcher stops, is denied.
func TestStreamKeyWatcher_AskDeniesWhenTheRequestEnds(t *testing.T) {
	keys, _ := typedKeys(t)
	w := startTestWatcher(keys, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if k := w.Ask(nil).Wait(ctx); k != 'n' {
		t.Errorf("canceled prompt got %q, want n", k)
	}
	w.Stop()
	if k := w.Ask(nil).Wait(context.Background()); k != 'n' {
		t.Errorf("prompt on a stopped watcher got %q, want n", k)
	}
}

func TestStreamKeyWatcher_NilIsSafe(t *testing.T) {
	var w *streamKeyWatcher
	w.Stop()
}

// Stop is reached from more than one path (the stream ending, the turn
// closing): a second call must be a no-op, not a double close.
func TestStreamKeyWatcher_StopTwiceIsSafe(t *testing.T) {
	keys, _ := typedKeys(t)
	w := startTestWatcher(keys, nil)
	w.Stop()
	w.Stop()
}

// openTTY opens a pty and returns its terminal side as a shell's stdin is
// (tty, and its fd) and the other side, where the test types the user's
// keys. Nothing of the process changes, so tests on their own terminals run
// side by side.
func openTTY(t *testing.T) (tty *os.File, fd int, keys *os.File) {
	t.Helper()
	ptmx, slave, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	// A dup is pollable the way fd 0 is; slave.Fd() would leave it blocking.
	// It is closed on exec, so another test's command never holds it open.
	fd, err = unix.FcntlInt(slave.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	tty = os.NewFile(uintptr(fd), "pty-stdin")
	t.Cleanup(func() {
		tty.Close()
		slave.Close()
		ptmx.Close()
	})
	return tty, fd, ptmx
}

// processStdin makes tty os.Stdin too until the test ends, for what reads
// the process's stdin whatever the shell's is: a picker (its own program).
// A test that calls it runs alone.
func processStdin(t *testing.T, tty *os.File) {
	t.Helper()
	saved := os.Stdin
	os.Stdin = tty
	t.Cleanup(func() { os.Stdin = saved })
}

// useTTY makes tty the terminal sh reads: its stdin, its key reader, and
// its executor's input.
func useTTY(sh *Shell, tty *os.File) {
	sh.stdin = tty
	sh.editorCfg.Input = editor.NewInputReader(tty)
	if sh.executor != nil {
		sh.executor.UseInput(tty)
	}
}

// A retried request starts its own watcher; the previous one must be
// stopped first, not left reading stdin beside it.
func TestTurn_RetryStopsThePreviousKeyWatcher(t *testing.T) {
	tty, _, _ := openTTY(t)
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	useTTY(sh, tty)
	turn := startTurn(t, sh)

	turn.request("first")
	first := turn.req.(*fullRequest).keys
	if first == nil {
		t.Fatal("no watcher on a terminal stdin")
	}
	turn.request("retry")
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("the first request's watcher still reads stdin beside the retry's")
	}
	turn.streamEnded()
}

// A permission prompt whose request ends under it (a timeout) must leave
// the terminal as the turn found it: echo and line editing back on. The
// prompt used to save the watcher's quiet mode and restore it after the
// watcher had already put the cooked mode back.
func TestTurn_PermissionPromptLeavesTheTerminalAsItFoundIt(t *testing.T) {
	tty, fd, _ := openTTY(t)
	before, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	useTTY(sh, tty)
	turn := startTurn(t, sh)
	turn.request("clean up")

	ctx, timeout := context.WithCancel(context.Background())
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		sh.handleToolPermission(ctx, agent.ToolPermissionRequest{Command: "rm -rf build", ToolName: "Bash", ToolCallID: "t1"})
	}()
	waitFor(t, func() bool { return strings.Contains(screen.String(), "Agent wants to run") })
	time.Sleep(30 * time.Millisecond) // the prompt is reading its key

	timeout()          // the request timed out under the prompt
	turn.streamEnded() // the main path puts the terminal back
	<-answered

	after, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	if after.Lflag != before.Lflag {
		t.Errorf("terminal left with lflag %#x (echo %v, icanon %v), want %#x",
			after.Lflag, after.Lflag&unix.ECHO != 0, after.Lflag&unix.ICANON != 0, before.Lflag)
	}
}

// typeOnPrompt is a screen that types a key the moment a permission prompt
// is drawn, and waits for the watcher to read it, as a quick user does.
type typeOnPrompt struct {
	lockedBuffer
	typing *os.File // where the keys are typed
	read   *os.File // the end the watcher reads
	typed  atomic.Bool
}

func (s *typeOnPrompt) Write(p []byte) (int, error) {
	n, err := s.lockedBuffer.Write(p)
	if strings.Contains(string(p), "Agent wants to run") && s.typed.CompareAndSwap(false, true) {
		s.typing.WriteString("y")
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			fds := []unix.PollFd{{Fd: int32(s.read.Fd()), Events: unix.POLLIN}}
			if n, _ := unix.Poll(fds, 0); n == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(5 * time.Millisecond) // the watcher has handled the key
	}
	return n, err
}

// A key typed as soon as the prompt appears was typed before the prompt
// could be read: it is dropped, not the answer, and the prompt says so
// (tooQuickNote). The watcher asks before the prompt is drawn, so the key
// is not kept for the next prompt either. A key typed once the keys
// settled answers.
func TestTurn_KeyTypedAsThePromptAppearsIsDroppedWithANote(t *testing.T) {
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	keys, typing := typedKeys(t)
	clock := typingAt(keys)
	screen := &typeOnPrompt{typing: typing, read: keys.File()}
	sh.agentOutput = NewAgentOutputCoordinator(screen)
	turn := startTurn(t, sh)
	turn.request("clean up")
	w := startTestWatcher(keys, nil)
	turn.mu.Lock()
	turn.req = &fullRequest{keys: w}
	turn.mu.Unlock()

	allowed := make(chan bool, 1)
	go func() {
		allow, _ := sh.handleToolPermission(questionCtx(t), agent.ToolPermissionRequest{Command: "pwd", ToolName: "Bash", ToolCallID: "t1"})
		allowed <- allow
	}()
	waitFor(t, func() bool { return strings.Contains(screen.String(), tooQuickNote) })
	select {
	case allow := <-allowed:
		t.Fatalf("the y typed as the prompt appeared answered it: allow %v", allow)
	default:
	}
	clock.advance(settleGap)
	typing.WriteString("y")
	if !<-allowed {
		t.Error("a y typed once the keys settled did not allow the command")
	}
	turn.streamEnded()
	if rest := keys.Typeahead(); len(rest) > 0 {
		t.Errorf("the next prompt gets %+v, want nothing", rest)
	}
}

// A shell made by New answers a permission prompt inside a turn through the
// turn's key watcher. Reading stdin itself there puts two readers on the
// terminal: the watcher swallows the answer as a stray key and the prompt
// waits until the request gives up.
func TestNew_PermissionKeyGoesThroughTheTurnsWatcher(t *testing.T) {
	sh := newShellForAgentCommand(t)
	turn := &liveTurn{}
	keys, _ := typedKeys(t)
	w := startTestWatcher(keys, nil)
	defer w.Stop()
	turn.req = &fullRequest{keys: w}

	_, release, _ := sh.claimPermissionKey(turn)
	defer release()
	w.mu.Lock()
	asking := w.asking != nil
	w.mu.Unlock()
	if !asking {
		t.Fatal("the watcher is not asking after the key was claimed: the prompt reads stdin beside the watcher")
	}
}

// Ctrl+O during a turn is a key the watcher keeps, not the terminal's
// discard: with IEXTEN on, macOS's line discipline took it as VDISCARD and
// threw away hash's output (the rest of the streamed reply) until the next
// ^O. The watcher turns IEXTEN off with echo and line buffering; signals
// stay on, so Ctrl+C is still SIGINT.
func TestStreamKeyWatcher_CtrlODoesNotDiscardOutput(t *testing.T) {
	tty, fd, user := openTTY(t)
	kb := keyboard{keys: editor.NewInputReader(tty)}
	w := kb.watch(func() {})
	if w == nil {
		t.Fatal("no watcher on the terminal")
	}
	defer w.Stop()
	tio, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	if tio.Lflag&unix.IEXTEN != 0 || tio.Lflag&unix.ISIG == 0 {
		t.Errorf("watching: IEXTEN on=%v, ISIG on=%v; want IEXTEN off, ISIG on", tio.Lflag&unix.IEXTEN != 0, tio.Lflag&unix.ISIG != 0)
	}

	user.WriteString("\x0f")
	waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return len(w.kept) == 1 })
	if _, err := unix.Write(fd, []byte("reply text")); err != nil {
		t.Fatal(err)
	}
	var got []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(string(got), "reply text") && time.Now().Before(deadline) {
		fds := []unix.PollFd{{Fd: int32(user.Fd()), Events: unix.POLLIN}}
		if n, _ := unix.Poll(fds, 50); n == 0 {
			continue
		}
		n, _ := user.Read(buf)
		got = append(got, buf[:n]...)
	}
	if !strings.Contains(string(got), "reply text") {
		t.Errorf("after Ctrl+O the terminal shows %q; the reply was discarded", got)
	}
}

// A permission prompt during a turn takes only a key typed once the keys
// settled: settleGap after the prompt asked and after the key before it.
// Any other key is dropped, never the answer and never the next prompt's,
// and the first one dropped draws the prompt's note. Esc answers at once
// (deny), never canceling the turn. The burst rule this replaces swallowed
// a deliberate answer, let an old held key put a later prompt into a burst,
// and let one prompt's answer start a burst for the next.
func TestStreamKeyWatcher_APromptTakesOnlyASettledKey(t *testing.T) {
	type step struct {
		after  time.Duration // since the key before it, or since the prompt asked
		key    editor.Key
		answer byte // 0: no answer
	}
	y, n, a := editor.Key{Rune: 'y'}, editor.Key{Rune: 'n'}, editor.Key{Rune: 'a'}
	esc := editor.Key{Special: editor.KeyEscape}
	for name, tc := range map[string]struct {
		typedBefore time.Duration // a key typed this long before the prompt asked; 0: none
		steps       []step
		notes       int32
	}{
		"a key as the prompt appears":     {0, []step{{100 * time.Millisecond, y, 0}, {settleGap, y, 'y'}}, 1},
		"mid typing, then a pause":        {30 * time.Millisecond, []step{{60 * time.Millisecond, a, 0}, {50 * time.Millisecond, editor.Key{Rune: 't'}, 0}, {settleGap - time.Millisecond, y, 0}, {settleGap, n, 'n'}}, 1},
		"settled after the prompt asked":  {0, []step{{settleGap, n, 'n'}}, 0},
		"an old key, long before":         {time.Hour, []step{{settleGap, y, 'y'}}, 0},
		"Esc acts at once":                {30 * time.Millisecond, []step{{10 * time.Millisecond, esc, 0x1b}}, 0},
		"quick y then Esc":                {0, []step{{10 * time.Millisecond, y, 0}, {10 * time.Millisecond, esc, 0x1b}}, 1},
		"an arrow does nothing":           {0, []step{{settleGap, editor.Key{Special: editor.KeyUp}, 0}, {settleGap, y, 'y'}}, 0},
		"the prompt asked, typing passes": {0, []step{{settleGap - time.Millisecond, a, 0}, {settleGap + time.Millisecond, y, 'y'}}, 1},
	} {
		keys, _ := typedKeys(t)
		clock := typingAt(keys)
		var escs, notes atomic.Int32
		w := newStreamKeyWatcher(keys, func() { escs.Add(1) })
		prev := time.Time{}
		if tc.typedBefore > 0 {
			at := clock.now()
			w.key(editor.Key{Rune: 'c'}, at, prev)
			prev = at
			clock.advance(tc.typedBefore)
		}
		ask := w.Ask(func() { notes.Add(1) })
		ask.drawn() // the prompt is on screen: Wait would say so
		for _, s := range tc.steps {
			clock.advance(s.after)
			at := clock.now()
			w.key(s.key, at, prev)
			prev = at
			var got byte
			select {
			case got = <-ask.answer:
			default:
			}
			if got != s.answer {
				t.Errorf("%s: %+v %v later: answer %q, want %q", name, s.key, s.after, got, s.answer)
			}
		}
		if notes.Load() != tc.notes {
			t.Errorf("%s: the note was drawn %d times, want %d", name, notes.Load(), tc.notes)
		}
		if escs.Load() != 0 {
			t.Errorf("%s: a key at the prompt canceled the turn", name)
		}
		want := 0
		if tc.typedBefore > 0 {
			want = 1 // only the key typed before the prompt is the next prompt's
		}
		if len(w.kept) != want {
			t.Errorf("%s: kept %+v for the next prompt, want %d key", name, w.kept, want)
		}
	}
}

// Two prompts back to back: the key that answered the first does not keep
// the second from taking a key typed once it settled.
func TestStreamKeyWatcher_BackToBackPrompts(t *testing.T) {
	keys, _ := typedKeys(t)
	clock := typingAt(keys)
	w := newStreamKeyWatcher(keys, func() {})
	first := w.Ask(nil)
	clock.advance(settleGap)
	at := clock.now()
	w.key(editor.Key{Rune: 'y'}, at, time.Time{})
	if k := <-first.answer; k != 'y' {
		t.Fatalf("first prompt got %q, want y", k)
	}
	clock.advance(150 * time.Millisecond) // the tool ran, the agent asks again
	second := w.Ask(nil)
	clock.advance(settleGap) // the user reads it and presses y
	w.key(editor.Key{Rune: 'y'}, clock.now(), at)
	select {
	case k := <-second.answer:
		if k != 'y' {
			t.Errorf("second prompt got %q, want y", k)
		}
	default:
		t.Errorf("a settled y did not answer the second prompt; kept %+v", w.kept)
	}
}

// The same through the terminal: "c" typed before the prompt, "at notes"
// as it appears. The prompt used to take the a, allowing the command for
// good; then it passed the keys on to the next prompt. They are dropped,
// and a key typed once the keys settled answers.
func TestStreamKeyWatcher_PermissionPromptMidTyping(t *testing.T) {
	keys, typed := typedKeys(t)
	clock := typingAt(keys)
	w := startTestWatcher(keys, func() {})
	typed.WriteString("c")
	waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return len(w.kept) == 1 })
	var notes atomic.Int32
	ask := w.Ask(func() { notes.Add(1) })
	got := make(chan byte, 1)
	ctx := questionCtx(t)
	go func() { got <- ask.Wait(ctx) }()
	typed.WriteString("at notes")
	clock.waitReadings(t, 2+int64(len("at notes"))) // c, the prompt asked, then each key
	waitFor(t, func() bool { return notes.Load() == 1 })
	select {
	case k := <-got:
		t.Fatalf("a key typed with the prompt answered it: %q", k)
	default:
	}
	if notes.Load() != 1 {
		t.Errorf("the note was drawn %d times, want once", notes.Load())
	}
	clock.advance(settleGap)
	typed.WriteString("n")
	select {
	case k := <-got:
		if k != 'n' {
			t.Errorf("prompt got %q, want n, typed once the keys settled", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("n, typed once the keys settled, did not answer the prompt")
	}
	w.Stop()
	if got := keys.Typeahead(); !slices.Equal(got, keysOf(t, "c")) {
		t.Errorf("the next prompt gets %+v, want only c, typed before the prompt", got)
	}
}
