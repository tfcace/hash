package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"

	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/termtext"
	"github.com/tfcace/hash/internal/trace"
)

// keyboard is the shell's one reader of the terminal. Every key hash reads
// goes through keys, the reader the editors share, so every reader follows
// one tokenizer, and a key one reader does not use is given back to the
// next (UnreadKeys) instead of being dropped or read again from raw bytes.
// Nothing is read before a reader asks for a key: what nobody asked for
// stays on the terminal, the input of the next program hash runs.
//
// One reader owns the keys at a time, holding the reader's claim
// (InputReader.Claim) from the handle it gets until it lets go; a second
// claim is refused, so two readers never split the keys:
//   - editing: an editor, for as long as it runs (Editor.Run), in raw mode
//     with the keyboard protocols on (the editor sets both);
//   - watching a turn: watch's *streamKeyWatcher, until Stop, while a ??
//     request runs. Echo, line buffering and IEXTEN are off and signals on, so
//     Ctrl+C stays SIGINT; Esc cancels the request and every other key is
//     kept, in order, for whatever reads next;
//   - answering: ask's *question, until close, for a question on screen
//     (the hint after a reply, a permission prompt outside a turn, !!'s
//     confirm, the first-run migration question, the `completions
//     generate` review), in raw mode, so Ctrl+C is a key;
//   - handed off: handOff's *handOff, until release, while a bubbletea
//     picker (Ctrl+R, Ctrl+P) reads the terminal itself. The picker's late
//     replies to its terminal queries reach the next editor, whose
//     tokenizer drops them.
//
// Typeahead is the next prompt's: keys typed before a question appeared
// never answer it. A question that changes something takes only a key
// typed once the keys settled (settleGap) and drops the keys typed sooner;
// a hint passes a key it has no use for on to the next prompt. The keys a
// command left unread are the next prompt's, all of them (typedDuring), as
// they are on bash's terminal. Of the keys a reader holds for the next
// prompt (a turn's watcher, a question), the next prompt gets those up to
// and including the first Enter, and the rest is dropped
// (holdForNextPrompt): they were typed
// for something hash cannot see (a password for the command that line
// runs, a line for a program reading its input), and running them as
// commands, or saving them in history, is unsafe. Keys cannot be pushed
// back to the terminal for that program (TIOCSTI is off on modern Linux
// and not portable). A picker hand-off drops every held key: the picker
// opens empty, and nothing typed for it lands on the line it hands back.
type keyboard struct {
	keys *editor.InputReader // nil: no terminal input to read (tests)
}

// newKeyReader is the shell's one key reader, on the terminal tty, with
// pastes cut at maxPaste bytes (0: the default).
func newKeyReader(tty *os.File, maxPaste uint) *editor.InputReader {
	keys := editor.NewInputReader(tty)
	keys.SetMaxPasteSize(maxPaste)
	return keys
}

// keyboard is the shell's keyboard: the reader its editors share.
func (s *Shell) keyboard() keyboard {
	return keyboard{keys: s.editorCfg.Input}
}

// tty is the terminal the keys come from, or nil when they come from
// something else.
func (k keyboard) tty() *os.File {
	if k.keys == nil {
		return nil
	}
	return k.keys.File()
}

// claim makes who the keys' one reader, or reports false when the keys
// do not come from a terminal (nobody can type) or another reader holds
// them.
func (k keyboard) claim(who string) (fd int, release func(), ok bool) {
	tty := k.tty()
	if tty == nil {
		return 0, nil, false
	}
	fd = int(tty.Fd())
	if !term.IsTerminal(fd) {
		return 0, nil, false
	}
	release, err := k.keys.Claim(who)
	if err != nil {
		trace.ShellHigh("keyboard_busy", map[string]any{"error": err.Error()})
		return 0, nil, false
	}
	return fd, release, true
}

// settleGap is how long the keys must have been still before a key
// answers a question that changes something (a permission prompt, !!'s
// confirm, the first-run migration, the `completions generate` review):
// the key must come at least this long after the question was drawn and
// after the key before it (settled). A key typed sooner was typed before
// the question could be read, for the next prompt (`a` would allow a
// command for good): it is dropped, never the answer and never the next
// prompt's, and the question says so (tooQuickNote).
const settleGap = 300 * time.Millisecond

// settled reports whether a key read at `at`, the key before it read at
// prev, comes late enough to answer a question drawn at drawn (settleGap).
// Every question and a turn's watcher read keys on the key reader's clock.
func settled(at, prev, drawn time.Time) bool {
	return at.Sub(drawn) >= settleGap && at.Sub(prev) >= settleGap
}

// actsAtOnce reports whether key acts on a question whenever it is typed:
// Esc and Ctrl+C only decline or cancel, so they never wait for the keys
// to settle.
func actsAtOnce(key editor.Key) bool {
	return key == editor.Key{Special: editor.KeyEscape} || key == editor.Key{Rune: 'c', Ctrl: true}
}

// tooQuickNote is what a question says the first time it drops a key typed
// before the keys settled.
const tooQuickNote = "too quick, answer again"

// question is the keyboard answering a question on screen: the terminal in
// raw mode, and the keys typed before the question appeared set aside.
// close gives them back, followed by the keys a hint passed on: they are
// the next prompt's.
type question struct {
	keys   *editor.InputReader
	typed  []editor.Key // typed before the question appeared, set aside
	passed []editor.Key // read at a hint but no use to it
	drawn  time.Time    // when the question appeared, on the reader's clock
	// note draws tooQuickNote, the first time answer drops a key (noted);
	// nil: the question draws nothing.
	note    func()
	noted   quickNote
	restore func() // the terminal mode, then the claim
}

// quickNote is where a question's tooQuickNote is.
type quickNote int

const (
	// quickNoteNone: no key was dropped, nothing is drawn.
	quickNoteNone quickNote = iota
	// quickNoteShown: answer dropped a key and drew the note.
	quickNoteShown
	// quickNoteErased: a line question's first character, or the line's
	// end, erased the note.
	quickNoteErased
)

// linePhase is where a line question is in its settle rule.
type linePhase int

const (
	// lineAwaitingSettled: the next key answers only once the keys settled
	// (answer); a key typed sooner is dropped.
	lineAwaitingSettled linePhase = iota
	// lineAnswering: a settled key came, and every key goes to the line,
	// however quick: emptying the line does not put the settle rule back.
	lineAnswering
)

// ask puts a question to the keyboard, or reports false when nobody can
// answer: the keys do not come from a terminal, or another reader holds
// them. Keys typed before the question appeared are set aside: they are the
// next prompt's, never its answer.
func (k keyboard) ask() (*question, bool) {
	fd, release, ok := k.claim("question")
	if !ok {
		return nil, false
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		release()
		return nil, false
	}
	return &question{
		keys:  k.keys,
		typed: k.keys.Typeahead(),
		drawn: k.keys.Now(),
		restore: func() {
			_ = term.Restore(fd, old)
			release()
		},
	}, true
}

// read reads the next key typed at the question, or reports false when ctx
// ended or the input failed first.
func (q *question) read(ctx context.Context) (editor.Key, bool) {
	key, err := q.keys.ReadKeyInterruptible(ctx.Done())
	return key, err == nil
}

// answer reads the next key that may answer a question that changes
// something: Esc and Ctrl+C whenever they come (actsAtOnce), any other key
// once the keys settled (settled). A key typed sooner is dropped, and the
// first one dropped draws the note.
func (q *question) answer(ctx context.Context) (editor.Key, bool) {
	for {
		prev := q.keys.LastRead()
		key, ok := q.read(ctx)
		if !ok || actsAtOnce(key) || settled(q.keys.LastRead(), prev, q.drawn) {
			return key, ok
		}
		if q.noted == quickNoteNone {
			if q.note != nil {
				q.note()
			}
			q.noted = quickNoteShown
		}
	}
}

// eraseNote takes a line question's note off the row once its answer's
// first character is typed or the line ends (the note sits after the
// cursor).
func (q *question) eraseNote(out io.Writer) {
	if q.noted == quickNoteShown {
		fmt.Fprint(out, "\x1b[K")
		q.noted = quickNoteErased
	}
}

// holdForNextPrompt gives keys that were read but not used back to the
// reader for the next prompt, up to and including the first Enter; the rest
// is dropped (see keyboard).
func holdForNextPrompt(r *editor.InputReader, keys []editor.Key) {
	for i, k := range keys {
		if k.Special == editor.KeyEnter && !k.Shift && !k.Alt {
			keys = keys[:i+1]
			break
		}
	}
	r.UnreadKeys(keys...)
}

// typedDuring gives the next prompt every key typed while a command ran
// that it left unread (executor's TakeUnread, bytes as the terminal
// queued them), before what is still on the terminal, as bash finds them
// on the terminal: a line typed across the command's end is one line, and
// several lines run in order.
func (k keyboard) typedDuring(typed []byte) {
	if k.keys == nil || len(typed) == 0 {
		return
	}
	r := editor.NewInputReader(bytes.NewReader(typed))
	var keys []editor.Key
	for key, err := r.ReadKey(); err == nil; key, err = r.ReadKey() {
		keys = append(keys, key)
	}
	k.keys.UnreadKeys(keys...)
}

// typedAhead reports whether keys were typed before the question
// appeared.
func (q *question) typedAhead() bool {
	return len(q.typed) > 0
}

// pass hands keys the question has no use for to the next prompt.
func (q *question) pass(keys ...editor.Key) {
	q.passed = append(q.passed, keys...)
}

// close ends the question: the set-aside keys, then the passed ones, go
// back to the reader, the terminal mode is restored, and the keys are free
// for the next reader.
func (q *question) close() {
	holdForNextPrompt(q.keys, append(q.typed, q.passed...))
	q.restore()
}

// confirm waits for the key that answers a hint of type ct and returns its
// action: ConfirmEnded when ctx ends first (hash terminated), which is no
// answer at all. A hint the user typed past during the turn is declined at
// once: they moved on, and what they typed is the next prompt's, not text
// for an edit or a reply that a later key would open.
func (k keyboard) confirm(ctx context.Context, ct ConfirmationType) ConfirmAction {
	q, ok := k.ask()
	if !ok {
		return ConfirmCancel
	}
	defer q.close()
	if q.typedAhead() {
		return ConfirmCancel
	}
	key, ok := q.read(ctx)
	if !ok {
		if ctx.Err() != nil {
			return ConfirmEnded
		}
		return ConfirmCancel // the input failed
	}
	action, pass := hintAction(key, ct)
	q.pass(pass...)
	return action
}

// hintAction is what a key does at a hint of type ct. Every key answers:
// one with no action there declines the hint and is passed on, the first
// key of the next line (an Alt chord too: the editor ignores one it does
// not bind). Esc declines. Ctrl+C declines, as Esc does: the reply is
// complete, so there is nothing left to cancel.
func hintAction(key editor.Key, ct ConfirmationType) (ConfirmAction, []editor.Key) {
	switch {
	case isEsc(key):
		return ConfirmCancel, nil
	case key == editor.Key{Special: editor.KeyEnter}:
		return ConfirmRun, nil // the primary action: run, finish, retry
	case key == editor.Key{Special: editor.KeyTab} && ct != ConfirmTypeError:
		return ConfirmEdit, nil // the secondary action: edit, copy
	case (key == editor.Key{Rune: 'r'} || key == editor.Key{Rune: 'R'}) && ct == ConfirmTypeExplanation:
		return ConfirmReply, nil
	case key == editor.Key{Rune: 'c', Ctrl: true}:
		return ConfirmCancel, nil
	}
	return ConfirmCancel, []editor.Key{key}
}

// permissionKey reads the key that answers a tool permission prompt
// outside a turn, as the byte permissionAnswerForKey reads (answerKey), or
// 'n' (deny) when ctx ends first. Only a key typed once the keys settled
// answers (answer); note draws tooQuickNote on the prompt when one came
// sooner. Keys that are no answer (arrows) do nothing.
func (k keyboard) permissionKey(ctx context.Context, note func()) byte {
	q, ok := k.ask()
	if !ok {
		return 'n'
	}
	defer q.close()
	q.note = note
	for {
		key, ok := q.answer(ctx)
		if !ok {
			return 'n'
		}
		if answer, ok := answerKey(key); ok {
			return answer
		}
	}
}

// yesAnswer is the answer to a yes/no question whose default is no.
type yesAnswer int

const (
	answeredNo        yesAnswer = iota // any line but y, Esc, Ctrl+D, no line at all
	answeredYes                        // y then Enter
	answerInterrupted                  // Ctrl+C: no, and the line that asked is interrupted
)

// yes reads the answer to a yes/no question whose default is no, as a
// line echoed to out. Keys typed before the question are not its answer.
func (k keyboard) yes(ctx context.Context, out io.Writer) yesAnswer {
	text, end := k.lineAnswer(ctx, out)
	switch {
	case end == lineInterrupted:
		return answerInterrupted
	case end == lineEntered && strings.EqualFold(text, "y"):
		return answeredYes
	}
	return answeredNo
}

// answerLine reads the line that answers a question hash printed on out,
// echoed to out. Keys typed before the question appeared are not its
// answer (ask), nor are keys typed before the keys settled while the line
// is empty (answer): a question whose default changes something (the
// first-run migration imports the user's config) must not take an Enter
// typed ahead, or typed with a quick `n`, as yes, nor the `completions
// generate` review an "a" typed while the agent drafted as accepting a
// draft not yet seen. It reports false when no line came: Esc, Ctrl+C or
// Ctrl+D, ctx ending (hash terminated), or no terminal.
func (k keyboard) answerLine(ctx context.Context, out io.Writer) (string, bool) {
	text, end := k.lineAnswer(ctx, out)
	return text, end == lineEntered
}

// lineAnswer is answerLine with how the line ended.
func (k keyboard) lineAnswer(ctx context.Context, out io.Writer) (string, lineEnd) {
	q, ok := k.ask()
	if !ok {
		return "", lineCanceled
	}
	defer q.close()
	q.note = func() { fmt.Fprint(out, lineNote) }
	return q.line(ctx, out)
}

// lineEnd is how a line question ended.
type lineEnd int

const (
	lineEntered     lineEnd = iota // Enter: the line is the answer
	lineCanceled                   // Esc, Ctrl+D, ctx ending, the input failing
	lineInterrupted                // Ctrl+C
)

// lineNote draws tooQuickNote dim after a line question's cursor, which
// stays where the answer goes; the answer's first character erases it.
// Autowrap is off while it draws: a note past the margin wrapping on the
// bottom row would scroll the screen, and ESC 8 would put the cursor back
// on the wrong row. Past the margin the note is cut instead.
const lineNote = "\x1b7\x1b[?7l\x1b[2m(" + tooQuickNote + ")\x1b[0m\x1b[?7h\x1b8"

// line reads a line typed at the question, echoed to out: Enter ends it
// and Backspace edits it. Until a key comes once the keys settled, keys
// typed sooner are dropped (answer, lineAwaitingSettled); from that key on
// every key goes to the line (lineAnswering). Esc and Ctrl+D cancel it, as
// do ctx ending and the input failing; Ctrl+C interrupts it. Other control,
// Alt and motion keys do nothing.
func (q *question) line(ctx context.Context, out io.Writer) (string, lineEnd) {
	var typed string
	phase := lineAwaitingSettled
	for {
		read := q.read
		if phase == lineAwaitingSettled {
			read = q.answer
		}
		key, ok := read(ctx)
		if !ok {
			return "", lineCanceled
		}
		phase = lineAnswering
		switch {
		case key == editor.Key{Special: editor.KeyEnter}:
			q.endLine(out)
			return typed, lineEntered
		case key == editor.Key{Rune: 'c', Ctrl: true}:
			q.endLine(out)
			return "", lineInterrupted
		case cancelsLine(key):
			q.endLine(out)
			return "", lineCanceled
		case key == editor.Key{Special: editor.KeyBackspace} && typed != "":
			cut := lastClusterStart(typed)
			w := termtext.Width(typed[cut:]) // 0 for a lone mark: the cell before is the prompt's
			typed = typed[:cut]
			fmt.Fprint(out, strings.Repeat("\b", w)+strings.Repeat(" ", w)+strings.Repeat("\b", w))
		default:
			text := lineText(key)
			if text == "" {
				continue
			}
			q.eraseNote(out)
			typed += text
			fmt.Fprint(out, text)
		}
	}
}

// endLine ends a line question's row: its note comes off if it still
// shows (eraseNote), then the newline.
func (q *question) endLine(out io.Writer) {
	q.eraseNote(out)
	fmt.Fprint(out, "\r\n")
}

// cancelsLine reports whether key cancels a line question: Esc, Ctrl+C,
// Ctrl+D.
func cancelsLine(key editor.Key) bool {
	return isEsc(key) || key == editor.Key{Rune: 'c', Ctrl: true} || key == editor.Key{Rune: 'd', Ctrl: true}
}

// lineText is the text key types into a line question: a paste's, its
// whitespace runs one space, or a printable character's. Other keys type
// nothing.
func lineText(key editor.Key) string {
	switch {
	case key.Special == editor.KeyPaste:
		return termtext.Inert(strings.Join(strings.Fields(key.PasteText), " "))
	case key.Special == editor.KeyNone && !key.Ctrl && !key.Alt && unicode.IsPrint(key.Rune):
		return string(key.Rune)
	}
	return ""
}

// typedAhead reports whether keys were typed for the next prompt: keys a
// reader held back (a turn's watcher kept them) or keys waiting on the
// terminal, read as a question reads its typeahead, and held for the next
// prompt as a question holds it (through the first Enter). A question that
// would open an editor on them (the conversation's reply box) does not
// open: they are the next prompt's, as at a hint (confirm).
func (k keyboard) typedAhead() bool {
	q, ok := k.ask()
	if !ok {
		return false
	}
	defer q.close()
	return q.typedAhead()
}

// lastClusterStart is where the last grapheme cluster of s starts: what
// Backspace erases, as the terminal drew it.
func lastClusterStart(s string) int {
	start := 0
	for end := termtext.ClusterEnd(s, 0); end < len(s); end = termtext.ClusterEnd(s, end) {
		start = end
	}
	return start
}

// handOff is the keys handed to a program that reads the terminal itself
// (a bubbletea picker): no reader of hash's reads them until release.
type handOff struct {
	release func()
}

// handOff hands the terminal to a picker and drops every key held for the
// next prompt (see keyboard). It reports false, dropping nothing, when the
// keys do not come from a terminal or another reader holds them; the picker
// runs anyway (it opens the terminal itself), and release is safe then.
func (k keyboard) handOff() (*handOff, bool) {
	_, release, ok := k.claim("picker")
	if !ok {
		return &handOff{release: func() {}}, false
	}
	k.keys.DropHeld() // only the holder touches the reader's keys
	return &handOff{release: release}, true
}
