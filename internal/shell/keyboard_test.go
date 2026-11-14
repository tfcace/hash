package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/editor"
)

// terminalShell is a shell whose terminal is a pty (its stdin),
// read through one key reader as New sets it up. It returns where the test
// types.
func terminalShell(t *testing.T) (*Shell, *os.File) {
	t.Helper()
	tty, _, typing := openTTY(t)
	return &Shell{
		stdin:      tty,
		responseUI: NewResponseUI(io.Discard),
		editorCfg:  editor.Config{Input: typingSlowly(editor.NewInputReader(tty))},
	}, typing
}

// testClock is a key reader's clock in a test: its time moves when the
// test moves it (advance), and by step at every reading.
type testClock struct {
	ns       atomic.Int64
	step     time.Duration
	readings atomic.Int64 // how often it was read: a key stamped, a question drawn
}

func (c *testClock) now() time.Time {
	c.readings.Add(1)
	return time.Unix(1000, c.ns.Add(int64(c.step)))
}

func (c *testClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

// waitReadings waits until the clock was read n times: every key typed so
// far was stamped, so advancing it now moves only the keys typed after.
func (c *testClock) waitReadings(t *testing.T, n int64) {
	t.Helper()
	waitFor(t, func() bool { return c.readings.Load() >= n })
}

// typingSlowly gives r a clock on which every key comes once the keys
// settled (settleGap after the last reading): a user who reads each
// question before answering it, whatever the machine's load.
func typingSlowly(r *editor.InputReader) *editor.InputReader {
	r.SetClock((&testClock{step: settleGap}).now)
	return r
}

// typingAt gives r a clock that moves only when the test advances it: keys
// typed between two advances are typed at once.
func typingAt(r *editor.InputReader) *testClock {
	c := &testClock{}
	r.SetClock(c.now)
	return c
}

// nextLine runs the prompt after a hint, the way the shell does, and
// returns the line it submits.
func nextLine(t *testing.T, sh *Shell) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := editor.New(editor.Config{Input: sh.editorCfg.Input}, sh.in(), io.Discard).Run(ctx)
	if err != nil || res.Exit != editor.ExitSubmit {
		t.Fatalf("next prompt = %+v, %v; want a submitted line", res, err)
	}
	return res.Text
}

// hintAnswer shows a hint of type ct and returns its answer, with type run
// once the hint reads its key.
func hintAnswer(t *testing.T, sh *Shell, ct ConfirmationType, typing func()) ConfirmAction {
	t.Helper()
	got := make(chan ConfirmAction, 1)
	ctx := questionCtx(t)
	go func() { got <- sh.keyboard().confirm(ctx, ct) }()
	time.Sleep(30 * time.Millisecond)
	typing()
	select {
	case action := <-got:
		return action
	case <-time.After(2 * time.Second):
		t.Fatal("the keys did not answer the hint")
		return 0
	}
}

// Esc at a command hint, then a line typed right after it: the Esc declines
// the agent's command and the line is the next prompt's. Bytes that came
// within 50ms of the Esc used to make it an escape sequence: the letters
// were dropped and the line's Enter ran the declined command.
func TestHint_EscThenFastTypingDeclinesAndStartsTheNextLine(t *testing.T) {
	t.Parallel()
	for _, ct := range []ConfirmationType{ConfirmTypeCommand, ConfirmTypeError, ConfirmTypeExplanation} {
		sh, typing := terminalShell(t)
		action := hintAnswer(t, sh, ct, func() {
			typing.WriteString("\x1b")
			time.Sleep(20 * time.Millisecond)
			typing.WriteString("echo X\r")
		})
		if action != ConfirmCancel {
			t.Fatalf("hint %v: Esc then typing = %v, want the hint declined", ct, action)
		}
		if line := nextLine(t, sh); line != "echo X" {
			t.Errorf("hint %v: next prompt ran %q, want \"echo X\"", ct, line)
		}
	}
}

// A key that does nothing at a hint declines it and is the first key of
// the next line: at an error hint `r` is not retry, at a command hint it is
// not reply.
func TestHint_KeyWithNoActionDeclinesAndIsTyped(t *testing.T) {
	for _, ct := range []ConfirmationType{ConfirmTypeCommand, ConfirmTypeError} {
		sh, typing := terminalShell(t)
		action := hintAnswer(t, sh, ct, func() { typing.WriteString("rm x\r") })
		if action != ConfirmCancel {
			t.Fatalf("hint %v: r = %v, want the hint declined", ct, action)
		}
		if line := nextLine(t, sh); line != "rm x" {
			t.Errorf("hint %v: next prompt ran %q, want \"rm x\"", ct, line)
		}
	}
}

// A confirmation hint waits for a key with the turn's context: when hash is
// terminated (SIGTERM cancels the shell's context) the wait ends at once,
// as the permission prompt's does, instead of blocking on stdin until a key.
// It is no answer, and says so apart from a decline (ConfirmEnded).
func TestHint_EndsWhenTheContextIsCanceled(t *testing.T) {
	t.Parallel()
	sh, _ := terminalShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan ConfirmAction, 1)
	go func() { got <- sh.keyboard().confirm(ctx, ConfirmTypeCommand) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case action := <-got:
		if action != ConfirmEnded {
			t.Errorf("action = %v, want ended, not a decline", action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the confirmation wait outlived its context")
	}
}

// The keys typed at a hint answer it.
func TestHint_KeysAnswerTheHint(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		keys string
		ct   ConfirmationType
		want ConfirmAction
	}{
		{"\r", ConfirmTypeCommand, ConfirmRun},
		{"\t", ConfirmTypeCommand, ConfirmEdit},
		{"\x1b", ConfirmTypeCommand, ConfirmCancel},
		{"\x03", ConfirmTypeCommand, ConfirmCancel},
		{"r", ConfirmTypeExplanation, ConfirmReply},
		{"\r", ConfirmTypeError, ConfirmRun},
	} {
		sh, typing := terminalShell(t)
		if got := hintAnswer(t, sh, tt.ct, func() { typing.WriteString(tt.keys) }); got != tt.want {
			t.Errorf("%q at hint %v = %v, want %v", tt.keys, tt.ct, got, tt.want)
		}
	}
}

// A line typed ahead during the turn means the user moved on: the hint is
// declined at once and the line is the next prompt's, never the hint's
// answer (an Enter typed ahead must not run a command the user has not
// seen) and never typed into the edit or reply a later key would open.
func TestHint_TypeaheadDeclinesIt(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	typing.WriteString("echo ahead\r")
	time.Sleep(20 * time.Millisecond)
	if got := hintAnswer(t, sh, ConfirmTypeCommand, func() { typing.WriteString("\t") }); got != ConfirmCancel {
		t.Fatalf("hint = %v, want it declined: the user typed past it", got)
	}
	if line := nextLine(t, sh); line != "echo ahead" {
		t.Errorf("next prompt ran %q, want the typeahead \"echo ahead\"", line)
	}
}

// What each key does at each hint.
func TestHintAction(t *testing.T) {
	enter, tab, esc := editor.Key{Special: editor.KeyEnter}, editor.Key{Special: editor.KeyTab}, editor.Key{Special: editor.KeyEscape}
	r, up := editor.Key{Rune: 'r'}, editor.Key{Special: editor.KeyUp}
	for _, tt := range []struct {
		key  editor.Key
		ct   ConfirmationType
		want ConfirmAction
		pass []editor.Key
	}{
		{enter, ConfirmTypeCommand, ConfirmRun, nil},
		{tab, ConfirmTypeCommand, ConfirmEdit, nil},
		{tab, ConfirmTypeExplanation, ConfirmEdit, nil},
		{tab, ConfirmTypeError, ConfirmCancel, []editor.Key{tab}},
		{r, ConfirmTypeExplanation, ConfirmReply, nil},
		{r, ConfirmTypeCommand, ConfirmCancel, []editor.Key{r}},
		{r, ConfirmTypeError, ConfirmCancel, []editor.Key{r}},
		{esc, ConfirmTypeError, ConfirmCancel, nil},
		{editor.Key{Rune: 'c', Ctrl: true}, ConfirmTypeCommand, ConfirmCancel, nil},
		{up, ConfirmTypeCommand, ConfirmCancel, []editor.Key{up}},
		// Alt chords, however the terminal sent them, are keys of their
		// own, not Esc then a key: passed on as typed. ESC CR is Alt+Enter
		// (Shift+Enter on some terminals).
		{editor.Key{Rune: 'e', Alt: true}, ConfirmTypeCommand, ConfirmCancel, []editor.Key{{Rune: 'e', Alt: true}}},
		{editor.Key{Special: editor.KeyBackspace, Alt: true}, ConfirmTypeCommand, ConfirmCancel, []editor.Key{{Special: editor.KeyBackspace, Alt: true}}},
		{editor.Key{Special: editor.KeyEnter, Alt: true}, ConfirmTypeCommand, ConfirmCancel, []editor.Key{{Special: editor.KeyEnter, Alt: true}}},
		{editor.Key{Special: editor.KeyLeft, Alt: true}, ConfirmTypeCommand, ConfirmCancel, []editor.Key{{Special: editor.KeyLeft, Alt: true}}},
		{editor.Key{Rune: 'e', Alt: true}, ConfirmTypeCommand, ConfirmCancel, []editor.Key{{Rune: 'e', Alt: true}}}, // kitty's CSI u Alt+e
	} {
		got, pass := hintAction(tt.key, tt.ct)
		if got != tt.want || !slices.Equal(pass, tt.pass) {
			t.Errorf("hintAction(%+v, %v) = %v, %+v; want %v, %+v", tt.key, tt.ct, got, pass, tt.want, tt.pass)
		}
	}
}

// A permission prompt outside a turn reads its answer through the shell's
// reader. Keys typed before it appeared are the next prompt's typeahead:
// the prompt used to discard them (drainStdin) and read the terminal past
// the reader. A key typed once the keys settled answers it.
func TestPermissionPromptOutsideATurn_KeepsTypeahead(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	typing.WriteString("echo ahead\r")
	time.Sleep(20 * time.Millisecond)
	wait, release, _ := sh.claimPermissionKey(nil)
	defer release()
	got := make(chan byte, 1)
	ctx := questionCtx(t)
	go func() { got <- wait(ctx) }()
	time.Sleep(30 * time.Millisecond)
	typing.WriteString("y")
	select {
	case k := <-got:
		if k != 'y' {
			t.Fatalf("prompt got %q, want y", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("y did not answer the prompt")
	}
	if line := nextLine(t, sh); line != "echo ahead" {
		t.Errorf("next prompt ran %q, want the typeahead \"echo ahead\"", line)
	}
}

// !!'s question after a successful line is answered by a key typed at it,
// not by the next line typed ahead: Scanln used to read that line as the
// answer, and the line was gone.
func TestIssueShortcut_TypeaheadDoesNotAnswer(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	typing.WriteString("echo ahead\r")
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	ctx := questionCtx(t)
	go func() { defer close(done); sh.handleIssueShortcut(ctx) }()
	time.Sleep(30 * time.Millisecond)
	typing.WriteString("n\r")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("n did not answer !!'s question")
	}
	if line := nextLine(t, sh); line != "echo ahead" {
		t.Errorf("next prompt ran %q, want the typeahead \"echo ahead\"", line)
	}
}

// !!'s question reads a line: y then Enter is yes, Backspace edits it, and
// any other line or Esc is no. An Alt key types nothing.
func TestKeyboardYes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		keys string
		want bool
	}{
		{"y\r", true},
		{"Y\r", true},
		{"n\r", false},
		{"\r", false},
		{"yes\r", false},
		{"n\x7fy\r", true},
		{"\x1b", false},
		{"\x03", false},
		{"\x04", false},
		{"\x1b[200~y\x1b[201~\r", true},
		// Alt+y as kitty and modifyOtherKeys encode it: Alt held is not y.
		{"\x1b[121;3u\r", false},
		{"\x1b[27;3;121~\r", false},
	} {
		sh, typing := terminalShell(t)
		got := make(chan bool, 1)
		ctx := questionCtx(t)
		go func() { got <- sh.keyboard().yes(ctx, io.Discard) == answeredYes }()
		time.Sleep(30 * time.Millisecond)
		typing.WriteString(tc.keys)
		select {
		case yes := <-got:
			if yes != tc.want {
				t.Errorf("%q = %v, want %v", tc.keys, yes, tc.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%q did not answer", tc.keys)
		}
	}
}

// Backspace in a line question erases the last character as drawn: a
// whole cluster (a letter with its combining mark), its cells on screen
// too. It used to drop only the mark, leaving the letter in the answer.
func TestQuestionLine_BackspaceErasesAWholeCluster(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		keys, want, erase string
	}{
		{"cafe\u0301\x7f\r", "caf", "\b \b"},
		{"x\U0001F44D\U0001F3FD\x7f\r", "x", "\b\b  \b\b"}, // thumbs up, skin tone: one wide cluster
		{"a\u65e5\x7f\r", "a", "\b\b  \b\b"},
	} {
		sh, typing := terminalShell(t)
		var out lockedBuffer
		got := make(chan string, 1)
		ctx := questionCtx(t)
		go func() {
			line, _ := sh.keyboard().answerLine(ctx, &out)
			got <- line
		}()
		time.Sleep(30 * time.Millisecond)
		typing.WriteString(tc.keys)
		select {
		case line := <-got:
			if line != tc.want {
				t.Errorf("%q: line %q, want %q", tc.keys, line, tc.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%q did not answer", tc.keys)
		}
		if !strings.Contains(out.String(), tc.erase) {
			t.Errorf("%q: echoed %q, want the erase %q", tc.keys, out.String(), tc.erase)
		}
	}
}

// At every question, Esc then a quick key is Esc (the ESC came alone):
// the permission prompt denies, !!'s question is no; the key, read with
// the Esc, is the next prompt's, as after a hint. ESC and the key in one write is an Alt
// chord, no Esc: the permission prompt denies (Alt+l is not l), and !!'s
// question types nothing, so its line is what follows. Both used to read
// the chord as Esc then the key, passing the key on to the next prompt.
func TestQuestions_EscThenAKeyAndAnAltChord(t *testing.T) {
	t.Parallel()
	ctx := questionCtx(t)
	for _, oneWrite := range []bool{false, true} {
		for name, ask := range map[string]func(sh *Shell) any{
			"permission": func(sh *Shell) any { return sh.keyboard().permissionKey(ctx, nil) },
			"line": func(sh *Shell) any {
				l, ok := sh.keyboard().answerLine(ctx, io.Discard)
				return fmt.Sprintf("%s %v", l, ok)
			},
		} {
			sh, typing := terminalShell(t)
			got := make(chan any, 1)
			go func() { got <- ask(sh) }()
			time.Sleep(30 * time.Millisecond)
			if oneWrite {
				typing.WriteString(map[string]string{"permission": "\x1bl", "line": "\x1bls\r"}[name])
			} else {
				typeQuickly(sh.editorCfg.Input, typing, "\x1b", "l")
			}
			want := map[string]any{"permission": byte(0x1b), "line": " false"}[name]
			if oneWrite {
				want = map[string]any{"permission": byte(0), "line": "s true"}[name]
			}
			select {
			case answer := <-got:
				if answer != want {
					t.Errorf("%s, one write %v: answer %v, want %v", name, oneWrite, answer, want)
				}
			case <-time.After(time.Second):
				t.Fatalf("%s, one write %v: no answer", name, oneWrite)
			}
			typing.WriteString("\r")
			next := ""
			if !oneWrite {
				next = "l" // read with the Esc, after it: the next prompt's, as at a hint
			}
			if line := nextLine(t, sh); line != next {
				t.Errorf("%s, one write %v: next prompt ran %q, want %q", name, oneWrite, line, next)
			}
		}
	}
}

// One reader owns the keys at a time: while a turn's watcher reads them a
// question, a picker hand-off or an editor is refused, and each gets them
// once the one before lets go. Two readers used to be kept apart only by
// call order.
func TestKeyboard_OneReaderAtATime(t *testing.T) {
	sh, _ := terminalShell(t)
	kb := sh.keyboard()
	refusedAll := func(holder string) {
		t.Helper()
		if q, ok := kb.ask(); ok {
			q.close()
			t.Errorf("a question was asked while the %s reads", holder)
		}
		if h, ok := kb.handOff(); ok {
			h.release()
			t.Errorf("the keys were handed off while the %s reads", holder)
		}
		if w := kb.watch(func() {}); w != nil {
			w.Stop()
			t.Errorf("a watcher started while the %s reads", holder)
		}
		ed := editor.New(editor.Config{Input: kb.keys}, kb.tty(), io.Discard)
		if _, err := ed.Run(questionCtx(t)); !errors.Is(err, editor.ErrInputBusy) {
			t.Errorf("an editor ran while the %s reads: %v", holder, err)
		}
	}

	w := kb.watch(func() {})
	if w == nil {
		t.Fatal("no watcher on the terminal")
	}
	refusedAll("watcher")
	w.Stop()

	q, ok := kb.ask()
	if !ok {
		t.Fatal("no question once the watcher stopped")
	}
	refusedAll("question")
	q.close()

	h, ok := kb.handOff()
	if !ok {
		t.Fatal("no hand-off once the question closed")
	}
	refusedAll("picker")
	h.release()
	if got := kb.keys.Owner(); got != "" {
		t.Errorf("after the hand-off the keys are held by %q", got)
	}
}

// keysOf is the keys the reader makes of s.
func keysOf(t *testing.T, s string) []editor.Key {
	t.Helper()
	keys, typed := typedKeys(t)
	typed.WriteString(s)
	typed.Close()
	var got []editor.Key
	for {
		k, err := keys.ReadKey()
		if err != nil {
			return got
		}
		got = append(got, k)
	}
}

// Keys typed during a turn are the next prompt's up to and including the
// first Enter; the rest is dropped. Keeping them all ran a sudo password
// typed for the command before it as a shell command, and saved it.
func TestStreamKeyWatcher_KeepsKeysThroughTheFirstEnter(t *testing.T) {
	keys, typed := typedKeys(t)
	w := startTestWatcher(keys, func() {})
	typed.WriteString("sudo ls\rs3cret\r")
	waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return len(w.kept) == 15 })
	w.Stop()
	if got, want := keys.Typeahead(), keysOf(t, "sudo ls\r"); !slices.Equal(got, want) {
		t.Errorf("the next prompt gets %+v, want only the first line %+v", got, want)
	}
}

// Typeahead a hint sets aside follows the same rule: the line after the
// first Enter (here the line a `cat` typed ahead would read) is dropped,
// never run at a later prompt.
func TestHint_TypeaheadPastTheFirstEnterIsDropped(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	typing.WriteString("cat\rCATLINE\r")
	time.Sleep(20 * time.Millisecond)
	if got := hintAnswer(t, sh, ConfirmTypeCommand, func() {}); got != ConfirmCancel {
		t.Fatalf("hint = %v, want it declined: the user typed past it", got)
	}
	if line := nextLine(t, sh); line != "cat" {
		t.Errorf("next prompt ran %q, want \"cat\"", line)
	}
	if held := sh.editorCfg.Input.Typeahead(); len(held) > 0 {
		t.Errorf("after the first line the reader still holds %+v", held)
	}
}

// Ctrl+R typed during a turn opens the picker at the next prompt; the keys
// typed after it are dropped, so the picker opens empty and nothing typed
// for it lands on (and submits) the line it hands back.
func TestRunPicker_DropsHeldKeys(t *testing.T) {
	sh, _ := terminalShell(t)
	sh.editorCfg.Input.UnreadKeys(keysOf(t, "deploy\r")...)
	if got, err := sh.runPicker(context.Background(), editor.Result{Exit: editor.ExitHistorySearch, Text: "ls"}); got != "ls" || err != nil {
		t.Errorf("runPicker = %q, %v; want the editor's line", got, err)
	}
	if held := sh.editorCfg.Input.Typeahead(); len(held) > 0 {
		t.Errorf("after the picker the reader holds %+v for the next prompt", held)
	}
}

// !!'s question waits with the shell's context: when hash is terminated
// (SIGTERM cancels it) the question ends at once. It used to wait with a
// context of its own, so hash stayed up until a key was typed.
func TestIssueShortcut_EndsWhenTheShellIsTerminated(t *testing.T) {
	t.Parallel()
	sh, _ := terminalShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sh.handleLine(ctx, "!!") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("!!'s question outlived the shell's context")
	}
}

// The `completions generate` review reads its answer and nothing past it:
// what follows the answer's Enter is the next reader's. A buffered reader
// on the terminal read ahead and threw the rest away with itself.
func TestCompletionsReview_ReadsNothingPastTheAnswer(t *testing.T) {
	sh, typing := terminalShell(t)
	review := newLineReview(func() (string, bool) { return sh.keyboard().answerLine(questionCtx(t), io.Discard) }, io.Discard)
	got := make(chan genChoice, 1)
	go func() { got <- review(true) }()
	time.Sleep(30 * time.Millisecond)
	typing.WriteString("q\recho next\r")
	select {
	case c := <-got:
		if c.action != genQuit {
			t.Fatalf("review = %+v, want quit", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("q did not answer the review")
	}
	if line := nextLine(t, sh); line != "echo next" {
		t.Errorf("next prompt ran %q, want \"echo next\"", line)
	}
}

// The review waits with the builtin's context: when hash is terminated the
// generator ends at once. A blocking read of the terminal kept it (and
// hash) up until a line was typed.
func TestCompletionsReview_EndsWhenTheShellIsTerminated(t *testing.T) {
	t.Parallel()
	sh, _ := terminalShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	gen := &pluginGenerator{
		ask:      func(context.Context, string) (string, error) { return "```toml\n" + validKubectlTOML + "```", nil },
		toolHelp: func(context.Context, string) string { return "help" },
		review:   newLineReview(func() (string, bool) { return sh.keyboard().answerLine(ctx, io.Discard) }, io.Discard),
		specsDir: t.TempDir(),
		out:      io.Discard,
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = gen.run(ctx, "kubectl", "") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the review outlived the shell's context")
	}
}

// Whether keys were typed ahead is one answer at every question: the keys
// a reader held back and the keys waiting on the terminal, read as a
// question reads them. The conversation's check saw only held keys, so a
// line typed after a follow-up's turn ended (still on the terminal) went
// into the reply box and on to the agent, where a hint declines on it.
func TestKeyboard_TypedAheadSeesKeysWaitingOnTheTerminal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ typed, next string }{
		{"ls\r", "ls"},
		{"ls\rCATLINE\r", "ls"},
	} {
		sh, typing := terminalShell(t)
		typing.WriteString(tc.typed)
		time.Sleep(20 * time.Millisecond)
		if !sh.keyboard().typedAhead() {
			t.Errorf("%q waiting on the terminal: typedAhead = false, want true", tc.typed)
			continue
		}
		if line := nextLine(t, sh); line != tc.next {
			t.Errorf("%q: the next prompt got %q, want %q", tc.typed, line, tc.next)
		}
		if held := sh.editorCfg.Input.Typeahead(); len(held) > 0 {
			t.Errorf("%q: after the first line the reader still holds %+v", tc.typed, held)
		}
	}
}

// A cluster drawn in no cells (a lone combining mark) erases no cell: the
// one before it is the prompt's. It used to erase one.
func TestQuestionLine_BackspaceOverAZeroWidthClusterErasesNothing(t *testing.T) {
	for _, zw := range []string{"\u0301", "\u20dd"} {
		sh, typing := terminalShell(t)
		var out lockedBuffer
		got := make(chan string, 1)
		ctx := questionCtx(t)
		go func() {
			line, _ := sh.keyboard().answerLine(ctx, &out)
			got <- line
		}()
		time.Sleep(30 * time.Millisecond)
		typing.WriteString(zw + "\x7f\r")
		select {
		case line := <-got:
			if line != "" {
				t.Errorf("%q: line %q, want it erased", zw, line)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%q did not answer", zw)
		}
		if want := zw + "\r\n"; out.String() != want {
			t.Errorf("%q: echoed %q, want %q (no cell erased)", zw, out.String(), want)
		}
	}
}

// A question keeps the keys typed before it apart from the keys it passes
// on: passing a key on does not make it typed ahead, and close gives back
// the typed keys first, then the passed ones, through the first Enter.
func TestQuestion_TypedAndPassedKeys(t *testing.T) {
	keys, _ := typedKeys(t)
	q := &question{keys: keys, restore: func() {}}
	q.pass(editor.Key{Rune: 'p'})
	if q.typedAhead() {
		t.Error("a passed key made the question typed ahead")
	}
	q = &question{keys: keys, typed: keysOf(t, "ls"), restore: func() {}}
	q.pass(keysOf(t, "x\ry")...)
	if !q.typedAhead() {
		t.Error("typed keys: typedAhead = false")
	}
	q.close()
	want := keysOf(t, "lsx\r")
	if got := readKeys(t, keys, len(want)); !slices.Equal(got, want) {
		t.Errorf("after close the reader gives %+v, want %+v", got, want)
	}
	if rest := keys.Typeahead(); len(rest) > 0 {
		t.Errorf("past the first Enter the reader holds %+v", rest)
	}
}

// A permission prompt outside a turn that appears in the middle of typing
// takes none of the keys typed before the keys settled: they are dropped,
// and the first one draws the note. The keys typed before it appeared are
// the next prompt's. It used to take the first key typed after it
// appeared, so "cat notes" typed across it answered with the a: allow this
// command for good.
func TestPermissionPromptOutsideATurn_TypingAcrossItNeverAnswers(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	clock := typingAt(sh.editorCfg.Input)
	typing.WriteString("c")
	time.Sleep(20 * time.Millisecond)
	var notes atomic.Int32
	got := make(chan byte, 1)
	ctx := questionCtx(t)
	go func() { got <- sh.keyboard().permissionKey(ctx, func() { notes.Add(1) }) }()
	clock.waitReadings(t, 2) // the question set the c aside and was drawn
	typing.WriteString("at notes")
	clock.waitReadings(t, 2+int64(len("at notes"))) // c, the question drawn, then each key
	select {
	case k := <-got:
		t.Fatalf("a key typed with the prompt answered it: %q", k)
	default:
	}
	clock.advance(settleGap)
	typing.WriteString("n")
	select {
	case k := <-got:
		if k != 'n' {
			t.Errorf("prompt got %q, want n, typed once the keys settled", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("n, typed once the keys settled, did not answer the prompt")
	}
	if notes.Load() != 1 {
		t.Errorf("the note was drawn %d times, want once", notes.Load())
	}
	typing.WriteString("\r")
	if line := nextLine(t, sh); line != "c" {
		t.Errorf("next prompt ran %q, want \"c\", typed before the prompt", line)
	}
}

// answerAsync asks a line question on sh's keyboard, echoed to out, and
// returns where its answer comes.
func answerAsync(t *testing.T, sh *Shell, out io.Writer) <-chan string {
	t.Helper()
	got := make(chan string, 1)
	ctx := questionCtx(t)
	go func() {
		line, ok := sh.keyboard().answerLine(ctx, out)
		if !ok {
			line = "(no line)"
		}
		got <- line
	}()
	return got
}

// noAnswer fails the test when got already holds an answer.
func noAnswer(t *testing.T, got <-chan string, what string) {
	t.Helper()
	select {
	case line := <-got:
		t.Fatalf("%s answered the question: %q", what, line)
	default:
	}
}

// A line question takes its first key only once the keys settled: a quick
// "n⏎" typed as it appears is dropped, both keys, and the question says so
// once, dim, after the cursor. A key typed once the keys settled starts
// the line, erasing the note, and the keys after it go to the line however
// quick. The burst rule this replaces sent such keys on to the next prompt.
func TestAnswerLine_TakesOnlyAKeyTypedOnceTheKeysSettled(t *testing.T) {
	sh, typing := terminalShell(t)
	clock := typingAt(sh.editorCfg.Input)
	var out lockedBuffer
	got := answerAsync(t, sh, &out)
	clock.waitReadings(t, 1) // the question was drawn
	typing.WriteString("n\r")
	clock.waitReadings(t, 3)
	noAnswer(t, got, `a quick "n⏎"`)
	waitFor(t, func() bool { return strings.Contains(out.String(), lineNote) })

	clock.advance(settleGap)
	typing.WriteString("y\r")
	select {
	case line := <-got:
		if line != "y" {
			t.Errorf("line %q, want y", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("y⏎ typed once the keys settled did not answer")
	}
	if want := lineNote + "\x1b[Ky\r\n"; out.String() != want {
		t.Errorf("echoed %q, want %q", out.String(), want)
	}
	if rest := sh.editorCfg.Input.Typeahead(); len(rest) > 0 {
		t.Errorf("the next prompt gets %+v: dropped keys reached it", rest)
	}
}

// !!'s confirm with a line typed ahead, unfinished: a y⏎ typed once the
// keys settled is yes, and the line typed ahead is the next prompt's.
func TestKeyboardYes_WithTypeahead(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	typing.WriteString("ls")
	time.Sleep(20 * time.Millisecond)
	got := make(chan bool, 1)
	ctx := questionCtx(t)
	go func() { got <- sh.keyboard().yes(ctx, io.Discard) == answeredYes }()
	time.Sleep(30 * time.Millisecond)
	typing.WriteString("y\r")
	select {
	case yes := <-got:
		if !yes {
			t.Error("y⏎ was no")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("y⏎ did not answer")
	}
	typing.WriteString("\r")
	if line := nextLine(t, sh); line != "ls" {
		t.Errorf("next prompt ran %q, want the typeahead \"ls\"", line)
	}
}

// Ctrl+C and Esc only decline: they act at once, however quickly they
// follow the question or the keys before them.
func TestAnswerLine_CtrlCAndEscActAtOnce(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"\x03", "\x1b"} {
		sh, typing := terminalShell(t)
		typingAt(sh.editorCfg.Input)
		typing.WriteString("ls")
		time.Sleep(20 * time.Millisecond)
		got := answerAsync(t, sh, io.Discard)
		time.Sleep(30 * time.Millisecond)
		typing.WriteString("x" + key)
		select {
		case line := <-got:
			if line != "(no line)" {
				t.Errorf("%q: line %q, want none", key, line)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%q typed quickly did not end the question", key)
		}
		if rest := sh.editorCfg.Input.Typeahead(); !slices.Equal(rest, keysOf(t, "ls")) {
			t.Errorf("%q: the next prompt gets %+v, want the typeahead ls", key, rest)
		}
	}
}

// The `completions generate` review with a key typed while the agent
// drafted: the key is the next prompt's, and the review's verb and its
// follow-up line, each typed once the keys settled, revise the draft. The
// held key used to put the review into a burst that swallowed the answer.
func TestTerminalReview_FollowUpWithAnOldHeldKey(t *testing.T) {
	t.Parallel()
	sh, typing := terminalShell(t)
	typing.WriteString("x") // typed while the agent drafted
	time.Sleep(20 * time.Millisecond)
	ctx := questionCtx(t)
	got := make(chan genChoice, 1)
	go func() {
		got <- reviewLine(func() (string, bool) { return sh.keyboard().answerLine(ctx, io.Discard) }, io.Discard, true)
	}()
	time.Sleep(30 * time.Millisecond)
	typing.WriteString("r\r")
	time.Sleep(30 * time.Millisecond) // the follow-up question is up
	typing.WriteString("add namespaces\r")
	select {
	case c := <-got:
		if c.action != genRevise || c.message != "add namespaces" {
			t.Errorf("review = %+v, want a revision \"add namespaces\"", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the follow-up did not answer the review")
	}
	if rest := sh.editorCfg.Input.Typeahead(); !slices.Equal(rest, keysOf(t, "x")) {
		t.Errorf("the next prompt gets %+v, want the held x", rest)
	}
}
