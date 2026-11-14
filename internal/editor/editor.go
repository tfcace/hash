// internal/editor/editor.go
package editor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/shlex"
	"github.com/tfcace/hash/internal/termtext"
	"github.com/tfcace/hash/internal/trace"
	"golang.design/x/clipboard"
	"golang.org/x/term"
)

// Completion represents a completion candidate.
type Completion struct {
	Text        string
	Description string
}

// completionMenu is the open completion menu. Editor.menu is nil while the
// menu is closed, so a closed menu cannot carry stale items or drill levels.
type completionMenu struct {
	items []Completion // never empty: a query with no items closes the menu
	index int          // selected item
	from  int          // byte offset in the buffer where the text the items replace starts
	word  string       // the word as this level opened: typed text or drilled path
	// hidden is what this level's listing left out of the matches.
	hidden CompletionHidden
	drill  []completionLevel
}

// completionLevel is a parent menu level, saved when drilling into a
// directory so Backspace can go back to it.
type completionLevel struct {
	word  string
	index int
	from  int
}

// lineFeedback is what the editor says under the line while no menu is
// open: completion's status, or that a paste was cut. The next key ends
// it, whatever it is: a notice is read once, and a Tab still fetching when
// the user moved on (even with Esc, or Left then Right back to the same
// spot) must not edit the line when the source lands.
type lineFeedback struct {
	kind feedbackKind
	// line and pos are where Tab was pressed, for feedbackFetching: a
	// source landing for other input is ignored.
	line string
	pos  int
}

type feedbackKind int

const (
	feedbackNone      feedbackKind = iota // nothing to say
	feedbackFetching                      // a matched source is loading; Tab is retried when it lands
	feedbackTimedOut                      // the request ran out of time
	feedbackNoMatches                     // nothing to complete
	feedbackWithheld                      // matches, none of which could be inserted safely
	feedbackPasteCut                      // the last paste was cut short
)

// notice is the text shown for the status, or "" for none.
func (s lineFeedback) notice() string {
	switch s.kind {
	case feedbackFetching:
		return "fetching completions..."
	case feedbackTimedOut:
		return "completion timed out"
	case feedbackNoMatches:
		return "no matches"
	case feedbackWithheld:
		return "no safe matches"
	case feedbackPasteCut:
		return "paste cut short: the rest was dropped"
	}
	return ""
}

// Config configures the editor.
type Config struct {
	// HistoryFunc: dir -1=prev, +1=next; currentLine is for saving; false = no change.
	HistoryFunc             func(dir int, currentLine string) (string, bool)
	CompleteOutcomeFunc     func(line string, pos int) CompletionOutcome // Tab completion: the candidates, or why there are none
	CompletionReadyCh       <-chan struct{}                              // Signals that a pending completion source landed
	AgentCompleteLine       func(line string) bool                       // Reports whether Tab should submit the line for agent completion
	PrefetchFunc            func(line string, pos int)                   // Background completion prefetch (on space)
	SuggestionFunc          func(input string) string                    // Inline suggestion from history (Fish-style)
	OnInputReady            func()                                       // Called after editor chrome is rendered, before input loop
	Gutter                  bool                                         // Show gutter indicator
	Prompt                  string                                       // Prompt string to display before input
	InputBgColor            string                                       // Background color for submitted input (hex)
	ScrollbarColor          string                                       // Foreground color for scrollbars (hex)
	DisableLineContinuation bool                                         // Disable shell-style line continuations on a typed newline
	// Incomplete reports whether text, read as rows a newline ends (as
	// the shell reads a line), is unfinished shell syntax (an open quote,
	// heredoc or compound command): Enter then adds a row instead of running
	// the line, and Shift+Enter inserts a plain newline. Nil: no text is
	// incomplete (an agent prompt, a reply).
	Incomplete           func(text string) bool
	InputFrame           *InputFrame // Optional frame for custom input rendering
	PreventEmptySubmit   bool        // Keep editor open when submitting an empty buffer
	DisableHistorySearch bool        // Disable Ctrl+R history search
	DisableContextPicker bool        // Disable Ctrl+P context picker
	Decline              Decline     // How the input is declined: by which keys, and what is left of it
	// Input is the key reader. The shell passes one that lives as long as
	// it does, so a key another reader gave back reaches the editor; New's
	// in is then only the terminal put in raw mode. Nil: the editor reads
	// in with a reader of its own.
	Input *InputReader
}

// CompletionOutcome is what a completion request came back with: a menu
// of items (MenuOutcome), or why there is none (EmptyOutcome). The zero
// value is "no matches".
type CompletionOutcome struct {
	items []Completion // never empty in a menu
	// from is the byte offset into the whole buffer where the text the
	// items replace starts, up to the cursor; it may be on an earlier row.
	from   int
	hidden CompletionHidden // what the menu leaves out
	status CompletionStatus // why there are no items
}

// Hiding is the menu o leaving out hidden; an empty outcome stays as it is.
func (o CompletionOutcome) Hiding(hidden CompletionHidden) CompletionOutcome {
	if len(o.items) > 0 {
		o.hidden = hidden
	}
	return o
}

// MenuOutcome is a menu of items that replace the buffer from byte offset
// from up to the cursor. With no items it is "no matches".
func MenuOutcome(items []Completion, from int) CompletionOutcome {
	if len(items) == 0 {
		return CompletionOutcome{}
	}
	return CompletionOutcome{items: items, from: from}
}

// EmptyOutcome is a request that came back without items, for status.
func EmptyOutcome(status CompletionStatus) CompletionOutcome {
	return CompletionOutcome{status: status}
}

// Items are the menu's items: none when the outcome is empty.
func (o CompletionOutcome) Items() []Completion { return o.items }

// From is where the text the menu's items replace starts.
func (o CompletionOutcome) From() int { return o.from }

// Hidden is what the menu leaves out of the matches.
func (o CompletionOutcome) Hidden() CompletionHidden { return o.hidden }

// Status is why the outcome has no items, or CompletionNoMatches for a menu.
func (o CompletionOutcome) Status() CompletionStatus { return o.status }

// CompletionStatus is why a completion request came back without items.
type CompletionStatus int

const (
	// CompletionNoMatches: nothing matched, or nothing completes this input.
	CompletionNoMatches CompletionStatus = iota
	// CompletionPending: a completer owns the input but its data has not
	// arrived yet. The editor says so and retries when CompletionReadyCh
	// fires.
	CompletionPending
	// CompletionTimedOut: the request ran out of time.
	CompletionTimedOut
	// CompletionWithheld: there were matches, but none could be inserted
	// safely at the cursor.
	CompletionWithheld
)

// ExitKind is why the editor returned.
type ExitKind int

const (
	ExitSubmit        ExitKind = iota // Enter: Text is the line to run
	ExitCanceled                      // Ctrl+C, or Escape where the Decline style declines with it
	ExitEOF                           // Ctrl+D on an empty line, or the input closed: exit the shell
	ExitHistorySearch                 // Ctrl+R: launch history search; Text is the line to return to
	ExitContextPicker                 // Ctrl+P: launch the context picker; Text is the line to return to
)

// Decline is how an editor's input is declined (left with ExitCanceled):
// which keys decline it, and what is left of it on screen.
type Decline int

const (
	// DeclineNone is the prompt's: Esc is a mode and menu key, and Ctrl+C
	// clears a line with text and declines an empty one, leaving the row as
	// drawn.
	DeclineNone Decline = iota
	// DeclineKeepRow is the agent's command or inline fill: Esc or one
	// Ctrl+C declines whatever the line holds, and the line stays on screen
	// with its last row ended.
	DeclineKeepRow
	// DeclineEraseRow is the conversation's reply box: Esc declines, Ctrl+C
	// clears a line with text first, and the input is erased.
	DeclineEraseRow
)

// Result is returned when the editor exits.
type Result struct {
	Text string
	Exit ExitKind
}

// Editor is the main editor instance.
type Editor struct {
	config  Config
	input   *InputReader
	display *Display
	state   *EditorState
	mode    Mode

	out io.Writer

	// Undo grouping: the kind of the open edit group (ActionNone when
	// closed) and when it was last extended.
	undoGroup   Action
	undoGroupAt time.Time

	clipboardInit bool // Lazy clipboard initialization

	// Under the line: the open completion menu, or else the feedback. One
	// shows at a time: openMenu ends the feedback, and setFeedback with
	// something to say closes the menu.
	menu     *completionMenu // nil while closed
	feedback lineFeedback

	// Ghost text state (inline suggestions), including any attached agent stream
	ghost *GhostText

	// screen is what the terminal shows of the editor's state. Handlers
	// only mark a change; the event loop redraws once per event, before it
	// waits for the next one, so no change (a late completion) is left
	// undrawn.
	screen screenState
}

// screenState is what the terminal shows of the editor's input.
type screenState int

const (
	screenCurrent screenState = iota // the last draw shows the editor's state
	screenStale                      // the state changed since: the loop redraws
	screenDone                       // the input was finalized or cleared: nothing more is drawn
)

// changed marks the screen stale: the loop redraws before the next event.
// Once the input is finalized or cleared it stays as it was left.
func (e *Editor) changed() {
	if e.screen == screenCurrent {
		e.screen = screenStale
	}
}

// New creates a new editor. Its keys come from cfg.Input, or else from a
// reader of its own on in; the terminal behind them is the one Run puts in
// raw mode.
func New(cfg Config, in io.Reader, out io.Writer) *Editor {
	state := NewEditorState()

	// All modes start in insert mode
	mode := NewInsertMode()

	// Default terminal size
	width, height := 80, 24
	if f, ok := out.(*os.File); ok {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil {
			width, height = w, h
		}
	}

	display := NewDisplay(out, width, height)
	display.SetGutter(cfg.Gutter)
	if cfg.InputBgColor != "" {
		display.SetInputBgColor(cfg.InputBgColor)
	}
	if cfg.ScrollbarColor != "" {
		display.SetScrollbarColor(cfg.ScrollbarColor)
	}
	if cfg.InputFrame != nil {
		display.SetFrame(cfg.InputFrame)
	}

	inputReader := cfg.Input
	if inputReader == nil {
		inputReader = NewInputReader(in)
	}

	state.LineContinuation = !cfg.DisableLineContinuation
	state.Incomplete = cfg.Incomplete
	state.AllowHistorySearch = !cfg.DisableHistorySearch
	state.AllowContextPicker = !cfg.DisableContextPicker
	state.Layout = display // visual-row vertical movement follows the render wrap

	return &Editor{
		config:  cfg,
		input:   inputReader,
		display: display,
		state:   state,
		mode:    mode,
		out:     out,
		ghost:   NewGhostText(),
	}
}

// SetGhostText sets inline suggestion text that appears after the cursor.
func (e *Editor) SetGhostText(text string) {
	e.ghost.Set(text)
}

// SetGhostTextStreaming sets up streaming ghost text from channels.
// Text chunks arrive on updates, errors on errCh. cancel (may be nil) stops
// the request behind them: the editor calls it when the user leaves the fill
// while it still streams (Esc, typing over it, Ctrl+C).
func (e *Editor) SetGhostTextStreaming(updates <-chan GhostStreamUpdate, errCh <-chan error, cancel func()) {
	e.ghost.startStream(updates, errCh, cancel)
	// Dismiss any active completion menu - ghost text takes precedence
	e.closeMenu()
}

// ClearGhostText removes any ghost text.
func (e *Editor) ClearGhostText() {
	e.ghost.Clear()
}

// SetInitialText sets the initial text in the editor buffer.
func (e *Editor) SetInitialText(text string) {
	e.setLine(text)
}

// setLine replaces the buffer with text, cursor at the end of its first line.
func (e *Editor) setLine(text string) {
	buf := NewBufferFromString(text)
	e.state.setBuffer(buf, Position{Row: 0, Col: len(buf.Line(0))})
}

// PrintSubmitted leaves text on screen the way Run leaves a submitted line,
// without reading any input. The shell uses it to show a command it runs on
// the user's behalf, so scrollback reads as if the user had typed it.
func (e *Editor) PrintSubmitted(text string) {
	e.display.SetGutter(e.config.Gutter)
	e.display.SetPrompt(e.config.Prompt)
	e.display.Finalize(NewBufferFromString(text))
}

// Run starts the editor and blocks until submit or cancel. The editor is
// its key reader's one reader while it runs: it fails with ErrInputBusy
// when another reader holds it.
func (e *Editor) Run(ctx context.Context) (Result, error) {
	release, err := e.input.Claim("editor")
	if err != nil {
		return Result{}, err
	}
	defer release()

	// Enable raw mode if we have a terminal
	cleanup := e.setupTerminalMode()
	if cleanup != nil {
		defer cleanup()
	}

	// Handle SIGWINCH for terminal resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// Configure display
	e.display.SetGutter(e.config.Gutter)
	e.display.SetPrompt(e.config.Prompt)

	// The first draw of a new live input
	e.screen = screenStale
	e.redraw()

	// Notify shell that input area is ready (for OSC 133;B)
	if e.config.OnInputReady != nil {
		e.config.OnInputReady()
	}

	// Read keys for this editor; whatever it does not take stays with the
	// reader for the next one.
	feed := startKeyFeed(e.input)
	defer feed.stop()

	return e.runEventLoop(ctx, sigCh, feed)
}

// keyboardModesOn asks the terminal to report Shift+Enter (and other
// modified keys) apart from their plain forms: kitty's keyboard protocol
// with the disambiguate flag (CSI > 1 u, pushed; Shift+Enter is CSI 13;2 u)
// and xterm's modifyOtherKeys level 2 (CSI > 4 ; 2 m; Shift+Enter is
// CSI 27;2;13 ~). A terminal with neither ignores both. keyboardModesOff
// pops the kitty flags and resets modifyOtherKeys for the commands that
// run next.
const (
	keyboardModesOn  = "\x1b[>1u\x1b[>4;2m"
	keyboardModesOff = "\x1b[<u\x1b[>4m"
)

// setupTerminalMode enables raw mode, on the terminal the keys come from,
// and enhanced keyboard protocols. Returns a cleanup function, or nil if
// the keys do not come from a terminal.
func (e *Editor) setupTerminalMode() func() {
	f := e.input.File()
	if f == nil || !term.IsTerminal(int(f.Fd())) {
		return nil
	}

	oldState, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return nil
	}
	e.out.Write([]byte(keyboardModesOn))
	e.out.Write([]byte("\x1b[?2004h")) // Enable bracketed paste mode

	return func() {
		e.out.Write([]byte("\x1b[?2004l")) // Disable bracketed paste
		e.out.Write([]byte(keyboardModesOff))
		term.Restore(int(f.Fd()), oldState)
	}
}

// keyFeed reads keys for one editor's event loop, one at a time and only
// when the loop asks (next): the reader outlives the editor, and nothing
// past the key that ends the editor is read. What follows the Enter stays
// on the terminal for whatever reads next (the command the line runs, a
// turn's Esc watcher). A key read when the feed stops goes back to the
// reader.
type keyFeed struct {
	want   chan struct{} // the loop asks for a key; buffered, so asking never blocks
	got    chan keyRead  // the key, or the read error (io.EOF: the input closed)
	asked  bool          // a key was asked for and has not come yet
	done   chan struct{}
	exited chan struct{}
}

// keyRead is one read of the feed: a key, or the error that ended the input.
type keyRead struct {
	key Key
	err error
}

func startKeyFeed(r *InputReader) *keyFeed {
	f := &keyFeed{
		want:   make(chan struct{}, 1),
		got:    make(chan keyRead),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	go f.run(r)
	return f
}

func (f *keyFeed) run(r *InputReader) {
	defer close(f.exited)
	for {
		select {
		case <-f.want:
		case <-f.done:
			return
		}
		key, err := r.ReadKeyInterruptible(f.done)
		if errors.Is(err, context.Canceled) {
			return
		}
		select {
		case f.got <- keyRead{key, err}:
		case <-f.done:
			if err == nil {
				r.UnreadKeys(key)
			}
			return
		}
	}
}

// next asks for the next key, unless one is already on its way, and
// returns the channel it comes on.
func (f *keyFeed) next() <-chan keyRead {
	if !f.asked {
		f.asked = true
		f.want <- struct{}{}
	}
	return f.got
}

// stop ends the feed and waits until it no longer reads the input, so the
// next reader (another editor, or bubbletea for Ctrl+R) does not race it for
// terminal replies.
func (f *keyFeed) stop() {
	close(f.done)
	<-f.exited
}

// runEventLoop is the main editor event loop.
func (e *Editor) runEventLoop(ctx context.Context, sigCh <-chan os.Signal, feed *keyFeed) (Result, error) {
	for {
		e.redraw()
		select {
		case <-ctx.Done():
			return Result{Exit: ExitCanceled}, ctx.Err()
		case <-sigCh:
			e.handleResize()
		case <-e.config.CompletionReadyCh:
			e.handleCompletionReady()
		case update, ok := <-e.ghost.updates:
			e.handleGhostStreamUpdate(update, ok)
		case err, ok := <-e.ghost.errs:
			e.handleGhostTextError(err, ok)
		case read := <-feed.next():
			feed.asked = false
			switch {
			case errors.Is(read.err, io.EOF):
				// No more input will ever come: the session is over, as with
				// Ctrl+D. A cancel would have the caller start a new editor on
				// the same closed input, forever.
				return Result{Text: e.state.Buffer.Content(), Exit: ExitEOF}, nil
			case read.err != nil:
				return Result{}, read.err
			}
			if result, done := e.handleKeyEvent(read.key); done {
				e.redraw() // an exit that leaves the input on screen leaves it current
				return result, nil
			}
		}
	}
}

// handleCompletionReady retries a Tab that was still fetching. A source
// landing for input the user has since moved on from is ignored, so the menu
// never opens over a line it does not describe. The cursor offset matters as
// much as the text: arrow keys leave the buffer unchanged, and a late result
// must not open at a different argument.
func (e *Editor) handleCompletionReady() {
	s := e.feedback
	if s.kind != feedbackFetching {
		return
	}
	e.setFeedback(lineFeedback{})
	e.changed() // the notice goes, whatever lands
	if s.line != e.state.Buffer.Content() || s.pos != e.cursorOffset() {
		return
	}
	before := snapshotOf(e.state.Buffer, e.state.Cursor)
	e.triggerCompletion() // may put a lone match in place
	e.recordUndo(before, ActionNone)
}

// handleGhostStreamUpdate processes streamed ghost text and activity together.
func (e *Editor) handleGhostStreamUpdate(update GhostStreamUpdate, ok bool) {
	e.changed()
	if !ok {
		e.ghost.updatesClosed()
		return
	}
	e.ghost.update(update)
	if update.Text != "" {
		e.closeMenu()
	}
}

// handleGhostTextError processes ghost text errors. The failed fill is
// replaced by a one-line notice after the cursor so the user knows why nothing
// arrived; the typed line stays editable and the notice goes on the next key.
func (e *Editor) handleGhostTextError(err error, ok bool) {
	e.changed()
	if !ok {
		e.ghost.errsClosed()
		return
	}
	if err != nil {
		e.ghost.fail(ghostNoticeFor(err))
	}
}

// ghostNoticeMaxWidth bounds the notice, in cells, so it stays on the input
// line.
const ghostNoticeMaxWidth = 72

// ghostNoticeFor turns a ghost stream error into the short notice shown after
// the cursor. A canceled request shows nothing: the user asked for that.
func ghostNoticeFor(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "agent request timed out"
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return termtext.Truncate("agent: "+strings.TrimSpace(msg), ghostNoticeMaxWidth, "…")
}

// handleKeyEvent processes a key event. Returns (result, true) if the editor should exit.
func (e *Editor) handleKeyEvent(key Key) (Result, bool) {
	trace.EditorDetailed("key_dispatch", map[string]any{
		"key":               keyString(key),
		"ghost_state":       int(e.ghost.state),
		"completion_active": e.menu != nil,
		"mode":              e.mode.Name(),
	})

	if key == (Key{}) {
		return Result{}, false // a discarded sequence is no keypress at all
	}
	e.changed() // every key at least ends the feedback under the line
	key = withoutUnboundCtrl(key)

	// Whatever the key changes becomes (part of) an undo step.
	before := snapshotOf(e.state.Buffer, e.state.Cursor)
	kind := ActionNone
	defer func() { e.recordUndo(before, kind) }()

	// Any keypress ends the completion status and a ghost notice.
	e.setFeedback(lineFeedback{})
	if e.ghost.state == ghostAgentFailed {
		e.ghost.Clear()
	}

	if result, done, handled := e.handleControlKey(key); handled {
		return result, done
	}

	if key.Special == KeyEscape && e.escDeclines() {
		return e.cancelInput(), true
	}

	// Handle ghost text interception
	if e.ghost.visible() {
		if e.handleGhostTextKey(key) {
			return Result{}, false
		}
		e.ghost.Clear()
		e.closeMenu()
	}

	// Handle completion menu interception
	if e.menu != nil {
		handled, menuKind := e.handleCompletionKey(key)
		if handled {
			kind = menuKind
			return Result{}, false
		}
	}

	// Delegate to mode and process result
	modeResult := e.mode.HandleKey(key, e.state)
	kind = modeResult.Action
	if key.PasteCut {
		e.setFeedback(lineFeedback{kind: feedbackPasteCut})
	}
	if result, done := e.processModeResult(modeResult); done {
		return result, true
	}

	e.state.Cursor.Clamp(e.state.Buffer)
	return Result{}, false
}

// withoutUnboundCtrl reads Ctrl+Enter and Ctrl+Escape as Enter and Escape.
// Terminals with kitty's flags or modifyOtherKeys report the Ctrl, legacy
// ones send the plain key, and nothing binds the modified forms.
func withoutUnboundCtrl(key Key) Key {
	if key.Ctrl && !key.Alt && !key.Shift && (key.Special == KeyEnter || key.Special == KeyEscape) {
		key.Ctrl = false
	}
	return key
}

func (e *Editor) handleControlKey(key Key) (Result, bool, bool) {
	if !key.Ctrl {
		return Result{}, false, false
	}

	switch key.Rune {
	case 'c':
		result, done := e.handleCtrlC()
		return result, done, true
	case 'd':
		if e.state.Buffer.Content() == "" {
			return Result{Exit: ExitEOF}, true, true
		}
		return Result{}, false, true
	default:
		return Result{}, false, false
	}
}

// escDeclines reports whether Esc now declines the input. An open menu or a
// history suggestion takes Esc first, as at the prompt; an agent's fill is
// what Esc declines.
func (e *Editor) escDeclines() bool {
	if e.config.Decline == DeclineNone || e.menu != nil {
		return false
	}
	return !e.ghost.visible() || e.ghost.fromAgent()
}

func (e *Editor) cancelInput() Result {
	e.closeMenu()
	e.ghost.Clear()
	switch e.config.Decline {
	case DeclineKeepRow:
		e.leaveInput()
	case DeclineEraseRow:
		e.clearInput()
	}
	return Result{Exit: ExitCanceled}
}

// handleCtrlC handles Ctrl+C key press.
func (e *Editor) handleCtrlC() (Result, bool) {
	// Ctrl+C while the agent fill streams abandons the whole request.
	if e.ghost.streaming() || e.state.Buffer.Content() == "" || e.config.Decline == DeclineKeepRow {
		return e.cancelInput(), true
	}
	e.closeMenu()
	e.ghost.Clear()
	e.state.setBuffer(NewBuffer(), Position{})
	e.setMode(NewInsertMode())
	return Result{}, false
}

// processModeResult handles the result from mode.HandleKey.
// Returns (result, true) if the editor should exit.
func (e *Editor) processModeResult(result ModeResult) (Result, bool) {
	if result.NewMode != nil {
		e.setMode(result.NewMode)
	}
	if res, done := e.runCommand(result.Command); done {
		return res, true
	}

	// Update inline suggestion after buffer changes
	if result.Action == ActionInsert || result.Action == ActionDelete || result.Action == ActionPaste {
		e.updateSuggestion()
	}
	return Result{}, false
}

// runCommand carries out a mode's command. Returns (result, true) if the
// editor should exit.
func (e *Editor) runCommand(cmd ModeCommand) (Result, bool) {
	switch cmd {
	case CommandSubmit:
		if e.config.PreventEmptySubmit && e.state.Buffer.Content() == "" {
			return Result{}, false
		}
		e.finalizeInput()
		return Result{Text: e.state.Buffer.Content(), Exit: ExitSubmit}, true
	case CommandHistorySearch:
		e.clearInput()
		return Result{Text: e.state.Buffer.Content(), Exit: ExitHistorySearch}, true
	case CommandContextPicker:
		e.clearInput()
		return Result{Text: e.state.Buffer.Content(), Exit: ExitContextPicker}, true
	case CommandComplete:
		return e.complete()
	case CommandPrefetch:
		e.triggerPrefetch()
	case CommandHistoryPrev:
		e.navigateHistory(-1)
	case CommandHistoryNext:
		e.navigateHistory(1)
	case CommandYank:
		e.yank()
	case CommandPaste, CommandPasteBefore:
		e.paste(cmd == CommandPasteBefore)
	}
	return Result{}, false
}

// complete handles Tab outside the menu. Returns (result, true) if the line
// is submitted for agent completion instead.
func (e *Editor) complete() (Result, bool) {
	// Tab on an inline ?? line submits it: the shell then runs the streaming
	// agent completion flow, which a synchronous completion race never could.
	if e.config.AgentCompleteLine != nil && e.config.AgentCompleteLine(e.state.Buffer.Content()) {
		e.finalizeInput()
		return Result{Text: e.state.Buffer.Content(), Exit: ExitSubmit}, true
	}
	e.triggerCompletion()
	return Result{}, false
}

// setMode switches the editing mode. The selection and the vertical goal
// column belong to the old mode's motions, so they go with it.
func (e *Editor) setMode(m Mode) {
	if e.inserting() == isInsertMode(m) {
		return
	}
	e.mode = m
	e.state.Cursor.ClearSelection()
	e.state.goal = nil
}

// inserting reports whether the editor is in insert mode.
func (e *Editor) inserting() bool {
	return isInsertMode(e.mode)
}

func isInsertMode(m Mode) bool {
	_, ok := m.(*InsertMode)
	return ok
}

// navigateHistory replaces the line with the previous (-1) or next (+1)
// history entry, if there is one.
func (e *Editor) navigateHistory(dir int) {
	if e.config.HistoryFunc == nil {
		return
	}
	if entry, ok := e.config.HistoryFunc(dir, e.state.Buffer.Content()); ok {
		e.setLine(entry)
	}
}

// updateSuggestion queries the SuggestionFunc and sets ghost text for inline suggestions.
func (e *Editor) updateSuggestion() {
	if e.config.SuggestionFunc == nil {
		return
	}

	// Don't overwrite agent ghost text
	if e.ghost.fromAgent() {
		return
	}

	content := e.state.Buffer.Content()

	// Skip for short input (need at least 2 chars)
	if len(content) < 2 {
		e.ghost.Clear()
		return
	}

	// Only suggest when cursor is at end of buffer
	lastRow := e.state.Buffer.LineCount() - 1
	lastCol := len(e.state.Buffer.Line(lastRow))
	if e.state.Cursor.Pos.Row != lastRow || e.state.Cursor.Pos.Col != lastCol {
		return
	}

	suggestion := e.config.SuggestionFunc(content)
	if suggestion != "" && strings.HasPrefix(suggestion, content) {
		suffix := suggestion[len(content):]
		if suffix != "" {
			e.ghost.Set(suffix)
			return
		}
	}

	// No match: clear the prediction
	e.ghost.Clear()
}

// redraw draws the editor if anything changed since the last draw.
func (e *Editor) redraw() {
	if e.screen == screenStale {
		e.render()
	}
}

// finalizeInput leaves the submitted line on screen and moves past it:
// nothing of the live input is left to redraw.
func (e *Editor) finalizeInput() {
	e.display.Finalize(e.state.Buffer)
	e.screen = screenDone
}

// leaveInput leaves the line on screen as it stands, without a ghost or a
// menu, and ends its last row, so what is written next starts below it.
func (e *Editor) leaveInput() {
	e.render()
	e.display.Leave()
	e.screen = screenDone
}

// clearInput takes the live input off the screen for good.
func (e *Editor) clearInput() {
	e.display.Clear()
	e.screen = screenDone
}

// render draws the editor's state: the input, then the menu or the
// feedback under it. Only the event loop (and Run's first draw) calls it;
// an input finalized or cleared is not drawn again.
func (e *Editor) render() {
	if e.screen == screenDone {
		return
	}
	e.screen = screenCurrent
	hasSelection := e.state.Cursor.HasSelection()
	e.display.SetNormalMode(!e.inserting())

	// Pass ghost text to display for inline rendering
	ghostText := ""
	if e.ghost.visible() {
		ghostText = e.ghost.Remaining()
	}
	e.display.SetRowsUnder(e.rowsUnderInput())
	e.display.RenderWithGhost(e.state.Buffer, e.state.Cursor, hasSelection, ghostText, GhostRenderState{
		Streaming: e.ghost.streaming(),
		FromAgent: e.ghost.fromAgent(),
		Status:    e.ghost.status,
		Notice:    e.ghost.notice,
	})

	// Under the input: the menu while it is open, else the feedback
	if m := e.menu; m != nil {
		displayItems := make([]CompletionItem, len(m.items))
		for i, item := range m.items {
			displayItems[i] = CompletionItem(item)
		}
		at := e.positionOf(m.from)
		e.display.RenderCompletionMenu(displayItems, m.index, at.Row, e.display.lineCells(at.Row, e.state.Buffer.Line(at.Row), at.Col), m.hidden.note())
	} else if notice := e.feedback.notice(); notice != "" {
		e.display.RenderCompletionNotice(notice)
	}
}

// rowsUnderInput is how many rows render draws under the input: the open
// menu's, or the feedback's one.
func (e *Editor) rowsUnderInput() int {
	if m := e.menu; m != nil {
		return completionMenuRows(len(m.items), m.hidden.note())
	}
	if e.feedback.notice() != "" {
		return 1
	}
	return 0
}

// handleGhostTextKey processes keys when ghost text is active.
// Returns true if the key was handled.
func (e *Editor) handleGhostTextKey(key Key) bool {
	// Modified Tab accepts one word at a time. Check this before plain Tab so
	// Alt/Ctrl+Tab does not get swallowed by the accept-all path.
	if key.Special == KeyTab && (key.Alt || key.Ctrl) {
		text := e.ghost.AcceptWord()
		trace.Agent("ghost_accept", map[string]any{
			"key":      "Alt/Ctrl+Tab",
			"accepted": text,
			"action":   "accept_word",
		})
		if text != "" {
			e.insertText(text)
		}
		return true
	}

	switch key.Special {
	case KeyTab:
		if !e.ghost.fromAgent() {
			// For history predictions: Tab dismisses ghost and triggers completion instead.
			// Use Right arrow to accept predictions (fish-style).
			e.ghost.Clear()
			return false // Fall through to completion/mode handler
		}
		// For agent suggestions: Tab accepts the full ghost text
		text := e.ghost.AcceptAll()
		trace.AgentHigh("ghost_accept", map[string]any{
			"key":      "Tab",
			"accepted": text,
			"action":   "accept_all",
		})
		if text != "" {
			e.insertText(text)
		}
		return true

	case KeyRight:
		// Right arrow accepts the full ghost text (fish-style)
		text := e.ghost.AcceptAll()
		trace.Agent("ghost_accept", map[string]any{
			"key":      "Right",
			"accepted": text,
			"action":   "accept_all",
		})
		if text != "" {
			e.insertText(text)
		}
		return true

	case KeyEscape:
		// Escape dismisses ghost text and stops streaming
		trace.AgentHigh("ghost_accept", map[string]any{
			"key":    "Escape",
			"action": "dismiss",
		})
		e.ghost.Clear()
		return true

	case KeyEnter:
		return e.handleGhostEnter()
	}

	// Any other key clears ghost text (handled by caller)
	trace.AgentDetailed("accept_blocked", map[string]any{
		"key":    keyString(key),
		"reason": "unhandled_key",
		"state":  "ghost_active",
	})
	return false
}

// handleGhostEnter is Enter with ghost text showing. It reports whether the
// key was used up; false lets Enter go on to submit the line.
func (e *Editor) handleGhostEnter() bool {
	if e.ghost.fromAgent() {
		if e.ghost.streaming() {
			if !e.ghost.acceptedAny() {
				// Nothing taken yet: Enter must not run the bare line
				// the fill is still completing.
				trace.AgentHigh("ghost_accept", map[string]any{
					"key":    "Enter",
					"action": "ignored_streaming",
				})
				return true
			}
			// Tab took part of the fill: run what was taken and drop
			// the rest, canceling the request.
			trace.AgentHigh("ghost_accept", map[string]any{
				"key":    "Enter",
				"action": "run_accepted_part",
			})
			e.ghost.Clear()
			return false
		}
		// Agent suggestions: Enter accepts the fill, then submits; a fill
		// with lines not drawn goes into the buffer, where they show, and
		// the next Enter runs it. Enter never runs text the user has not seen.
		whole := e.ghost.shownWhole()
		text := e.ghost.AcceptAll()
		trace.AgentHigh("ghost_accept", map[string]any{
			"key":      "Enter",
			"accepted": text,
			"action":   "accept_and_run",
			"review":   !whole,
		})
		if text != "" {
			e.insertText(text)
		}
		return !whole // false lets Enter go on to submit
	}
	// Predictions: Enter dismisses ghost and submits what's typed (fish-style)
	trace.AgentHigh("ghost_accept", map[string]any{
		"key":    "Enter",
		"action": "dismiss_and_submit",
	})
	e.ghost.Clear()
	// Don't return true - let Enter propagate to submit
	return false
}

// insertText inserts text at the cursor position.
func (e *Editor) insertText(text string) {
	row, col := e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col
	e.state.Buffer.Insert(row, col, text)
	e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col = cursorAfterInsert(row, col, text)
}

func (e *Editor) handleResize() {
	if f, ok := e.out.(*os.File); ok {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil {
			e.display.Resize(w, h)
		}
	}
	e.changed()
}

// undoGroupWindow is how long a pause may be inside one typing or deleting
// undo step.
const undoGroupWindow = 300 * time.Millisecond

// recordUndo files what one event did to the text. before is the state from
// just before it. Typing and deleting extend an open group of the same kind;
// any other change is a step of its own, and an event that changes nothing
// (a motion, a mode switch, undo itself) closes the open group.
func (e *Editor) recordUndo(before Snapshot, kind Action) {
	if kind == ActionUndo || before.Buffer.sameText(e.state.Buffer) {
		e.undoGroup = ActionNone
		return
	}
	now := time.Now()
	groups := kind == ActionInsert || kind == ActionDelete
	if !groups || kind != e.undoGroup || now.Sub(e.undoGroupAt) > undoGroupWindow {
		e.state.UndoStack.Push(before)
	}
	e.undoGroup, e.undoGroupAt = ActionNone, now
	if groups {
		e.undoGroup = kind
	}
}

// initClipboard initializes the clipboard on first use.
func (e *Editor) initClipboard() {
	if !e.clipboardInit {
		_ = clipboard.Init()
		e.clipboardInit = true
	}
}

// yank copies the selected text to the system clipboard.
func (e *Editor) yank() {
	if !e.state.Cursor.HasSelection() {
		return
	}

	e.initClipboard()

	start, end := e.state.Cursor.SelectionRange(e.state.Buffer)
	text := e.extractText(start, end)
	// A clipboard that cannot be written leaves nothing to report from the
	// editor; the selection is still cleared below, matching the old behavior.
	_, _ = clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))

	// Clear selection after yank (Helix behavior)
	e.state.Cursor.ClearSelection()
}

// paste inserts text from the system clipboard at the cursor position.
func (e *Editor) paste(before bool) {
	e.initClipboard()

	data, err := clipboard.Read(context.Background(), clipboard.FmtText)
	if err != nil || len(data) == 0 {
		// ErrNoData, or no usable clipboard: nothing to paste.
		return
	}

	e.pasteText(string(data), before)
}

// pasteText inserts text at the cursor, or after the character under it
// unless before is set, and leaves the cursor after the pasted text.
func (e *Editor) pasteText(text string, before bool) {
	text = normalizePastedText(text)
	row, col := e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col
	if !before {
		// Move past current character for 'p' (paste after)
		if line := e.state.Buffer.Line(row); col < len(line) {
			col = nextClusterBoundary(line, col)
		}
	}
	e.state.Buffer.Insert(row, col, text)
	e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col = cursorAfterInsert(row, col, text)
}

// triggerCompletion fetches completions and activates the menu if needed.
func (e *Editor) triggerCompletion() {
	e.completeWord(false)
}

// refineCompletion re-queries after the user edited the word with the menu
// open. Unlike a fresh Tab, a single remaining candidate keeps the menu open
// instead of auto-inserting: the user is mid-word and may keep typing.
func (e *Editor) refineCompletion() {
	e.completeWord(true)
}

func (e *Editor) completeWord(refine bool) {
	if e.config.CompleteOutcomeFunc == nil {
		return
	}

	line := e.state.Buffer.Content()
	pos := e.cursorOffset()

	outcome := e.fetchCompletions(line, pos)
	items := outcome.items
	if len(items) == 0 {
		e.closeMenu()
		e.setFeedback(feedbackFor(outcome.status, line, pos))
		return
	}

	from := e.replaceFrom(outcome)

	if refine && e.menu != nil {
		// Narrowing keeps the level (and its drill history) and re-lists.
		e.menu.items, e.menu.index, e.menu.from, e.menu.hidden = items, 0, from, outcome.hidden
		return
	}

	e.closeMenu()
	if len(items) == 1 && !outcome.hidden.any() {
		// A lone match, file or directory, goes in place. For a directory the
		// next Tab lists its children, so there is no need for a one-row menu
		// that costs an extra Enter before the line can run. One that is
		// alone because others were left out is not the only match: it
		// shows in a menu that says so.
		e.replaceWord(from, items[0].Text)
		return
	}

	e.openMenu(&completionMenu{items: items, from: from, word: e.wordBeforeCursor(from), hidden: outcome.hidden})
}

// fetchCompletions asks the configured completer about line at pos.
func (e *Editor) fetchCompletions(line string, pos int) CompletionOutcome {
	if e.config.CompleteOutcomeFunc == nil {
		return CompletionOutcome{}
	}
	return e.config.CompleteOutcomeFunc(line, pos)
}

// feedbackFor is the feedback for a request at line and pos that came back
// empty. A pending one remembers where Tab was pressed, so a late arrival
// for input the user moved on from is discarded.
func feedbackFor(st CompletionStatus, line string, pos int) lineFeedback {
	switch st {
	case CompletionPending:
		return lineFeedback{kind: feedbackFetching, line: line, pos: pos}
	case CompletionTimedOut:
		return lineFeedback{kind: feedbackTimedOut}
	case CompletionWithheld:
		return lineFeedback{kind: feedbackWithheld}
	}
	return lineFeedback{kind: feedbackNoMatches}
}

// triggerPrefetch calls the prefetch function to populate completion cache.
// Called when space is typed after a command.
func (e *Editor) triggerPrefetch() {
	if e.config.PrefetchFunc == nil {
		return
	}

	line := e.state.Buffer.Content()
	pos := e.cursorOffset()

	// Only prefetch if line ends with space (just typed a space)
	if pos > 0 && pos <= len(line) && line[pos-1] == ' ' {
		e.config.PrefetchFunc(line, pos)
	}
}

// cursorOffset returns the byte offset of cursor in the buffer content.
func (e *Editor) cursorOffset() int {
	offset := 0
	for i := 0; i < e.state.Cursor.Pos.Row; i++ {
		offset += len(e.state.Buffer.Line(i)) + 1 // +1 for newline
	}
	offset += e.state.Cursor.Pos.Col
	return offset
}

// replaceFrom is where the text an outcome's items replace starts: where
// the completer says, which may be on an earlier row or inside the word the
// editor sees. A start past the cursor (a completer's bug) is the word at
// the cursor.
func (e *Editor) replaceFrom(outcome CompletionOutcome) int {
	if pos := e.cursorOffset(); outcome.from >= 0 && outcome.from <= pos {
		return outcome.from
	}
	return e.findWordStart()
}

// findWordStart returns the byte offset in the buffer where the word at the
// cursor starts: words split at unquoted blanks and newlines, the rule
// completion uses (shlex.WordStart), over the whole buffer, so a quoted word
// may start on an earlier row.
func (e *Editor) findWordStart() int {
	return shlex.WordStart(e.state.Buffer.Content(), e.cursorOffset())
}

// wordBeforeCursor returns the buffer from offset from up to the cursor.
func (e *Editor) wordBeforeCursor(from int) string {
	end := e.cursorOffset()
	if from < 0 || from > end {
		return ""
	}
	return e.state.Buffer.Content()[from:end]
}

// positionOf is the buffer position of byte offset off in its content.
func (e *Editor) positionOf(off int) Position {
	for row := 0; row < e.state.Buffer.LineCount(); row++ {
		n := len(e.state.Buffer.Line(row))
		if off <= n {
			return Position{row, off}
		}
		off -= n + 1
	}
	last := e.state.Buffer.LineCount() - 1
	return Position{last, len(e.state.Buffer.Line(last))}
}

// replaceWord replaces the buffer from offset from to the cursor with text
// and leaves the cursor after it.
func (e *Editor) replaceWord(from int, text string) {
	start := e.positionOf(from)
	e.state.Buffer.Delete(start, e.state.Cursor.Pos)
	e.state.Buffer.Insert(start.Row, start.Col, text)
	e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col = cursorAfterInsert(start.Row, start.Col, text)
}

// acceptCompletion replaces the current word with the selected completion
// and closes the menu.
func (e *Editor) acceptCompletion(item Completion) {
	e.replaceWord(e.menu.from, item.Text)
	e.closeMenu()
}

// drillIntoDirectory replaces the current word with the directory path
// and re-lists the menu with its contents.
func (e *Editor) drillIntoDirectory(item Completion) {
	m := e.menu
	m.drill = append(m.drill, completionLevel{word: m.word, index: m.index, from: m.from})
	e.replaceWord(m.from, item.Text)

	outcome, ok := e.relist()
	if !ok {
		return // an empty directory is accepted as-is
	}
	m.items, m.index, m.word, m.from, m.hidden = outcome.items, 0, item.Text, e.replaceFrom(outcome), outcome.hidden
	items := outcome.items

	// Auto-drill single-child directories (with depth limit to prevent
	// stack overflow), but not past a child alone because others were left
	// out.
	if len(items) == 1 && !m.hidden.any() && strings.HasSuffix(items[0].Text, "/") && len(m.drill) <= 20 {
		e.drillIntoDirectory(items[0])
	}
}

// relist lists the word at the cursor for a menu level that just changed
// (drilled in or up). An empty listing closes the menu, saying why when the
// listing is still loading or timed out rather than empty, and reports
// false.
func (e *Editor) relist() (CompletionOutcome, bool) {
	line, pos := e.state.Buffer.Content(), e.cursorOffset()
	outcome := e.fetchCompletions(line, pos)
	if len(outcome.items) > 0 {
		return outcome, true
	}
	e.closeMenu()
	if outcome.status != CompletionNoMatches {
		e.setFeedback(feedbackFor(outcome.status, line, pos))
	}
	return outcome, false
}

// drillUp goes back to the parent menu level: the word returns to what it
// was before the drill and the parent's listing and selection come back.
func (e *Editor) drillUp() {
	m := e.menu
	if m == nil || len(m.drill) == 0 {
		return
	}
	prev := m.drill[len(m.drill)-1]
	m.drill = m.drill[:len(m.drill)-1]

	e.replaceWord(prev.from, prev.word)
	outcome, ok := e.relist()
	if !ok {
		return
	}
	items := outcome.items
	m.items, m.from, m.word, m.hidden = items, prev.from, prev.word, outcome.hidden
	m.index = prev.index
	if m.index >= len(items) {
		m.index = 0
	}
}

// setFeedback is the one transition of the feedback under the line. What
// it says shows with the menu closed, so it closes the menu; ending the
// feedback (feedbackNone) leaves an open menu as it is.
func (e *Editor) setFeedback(s lineFeedback) {
	if s.kind != feedbackNone {
		e.closeMenu()
	}
	e.feedback = s
}

// openMenu opens the completion menu m, which takes the place of any
// feedback under the line.
func (e *Editor) openMenu(m *completionMenu) {
	e.feedback = lineFeedback{}
	e.menu = m
}

// closeMenu closes the completion menu. It is the only way the menu closes;
// the next redraw clears its rows with everything below the input.
func (e *Editor) closeMenu() {
	e.menu = nil
}

// handleCompletionKey processes keys when completion menu is active.
// Returns true if the key was handled, false to pass through, and for typing
// or deleting inside the menu the matching undo kind.
func (e *Editor) handleCompletionKey(key Key) (bool, Action) {
	m := e.menu
	switch key.Special {
	case KeyDown, KeyTab, KeyUp:
		e.moveInMenu(key)
		return true, ActionNone

	case KeyEnter:
		// Enter always accepts the selected item and closes the menu
		e.acceptCompletion(m.items[m.index])
		return true, ActionNone

	case KeyRight:
		if selected := m.items[m.index]; strings.HasSuffix(selected.Text, "/") {
			e.drillIntoDirectory(selected)
		} else {
			e.acceptCompletion(selected)
		}
		return true, ActionNone

	case KeyEscape:
		e.closeMenu()
		return true, ActionNone

	case KeyBackspace:
		return e.completionBackspace()

	default:
		// Printable characters narrow the open menu: the character is typed
		// into the buffer as usual and the completion re-queries, so the menu
		// filters like a search list using the configured matching mode
		// (prefix or fuzzy). Space and non-insert modes fall through to
		// normal editing instead.
		if key.Special == 0 && key.Rune > ' ' && !key.Ctrl && !key.Alt && e.inserting() {
			e.insertText(string(key.Rune))
			e.refineCompletion()
			return true, ActionInsert
		}

		// Anything else: dismiss and pass through
		e.closeMenu()
		return false, ActionNone
	}
}

// moveInMenu moves the selection: Tab and Down to the next row, Shift+Tab
// and Up to the one before. On a menu narrowed to one row Tab accepts it,
// as it does a lone match when it first lists the word.
func (e *Editor) moveInMenu(key Key) {
	m := e.menu
	if key.Special == KeyTab && !key.Shift && len(m.items) == 1 {
		e.acceptCompletion(m.items[0])
		return
	}
	step := 1
	if key.Special == KeyUp || key.Shift {
		step = -1
	}
	m.index = (m.index + step + len(m.items)) % len(m.items)
}

// completionBackspace un-narrows the menu, or drills back up once the word is
// the drilled directory again, or closes the menu and lets Backspace edit.
func (e *Editor) completionBackspace() (bool, Action) {
	m := e.menu
	switch {
	case len(m.drill) > 0 && e.wordBeforeCursor(m.from) == m.word:
		e.drillUp()
		return true, ActionNone
	case e.inserting() && e.cursorOffset() > m.from:
		// Un-narrow: delete the last typed character and re-query, the
		// mirror of printable keys narrowing the menu.
		e.deleteCharBeforeCursor()
		e.refineCompletion()
		return true, ActionDelete
	default:
		// At the word start (or not inserting): dismiss
		e.closeMenu()
		return false, ActionNone
	}
}

// deleteCharBeforeCursor removes the character (grapheme cluster) just
// before the cursor on the current line.
func (e *Editor) deleteCharBeforeCursor() {
	row := e.state.Cursor.Pos.Row
	col := e.state.Cursor.Pos.Col
	line := e.state.Buffer.Line(row)
	if col <= 0 || col > len(line) {
		return
	}
	from := previousClusterBoundary(line, col)
	e.state.Buffer.Delete(Position{row, from}, Position{row, col})
	e.state.Cursor.Pos.Col = from
}

// extractText extracts text between two positions.
func (e *Editor) extractText(start, end Position) string {
	if start.Row == end.Row {
		line := e.state.Buffer.Line(start.Row)
		if end.Col > len(line) {
			end.Col = len(line)
		}
		if start.Col > len(line) {
			start.Col = len(line)
		}
		return line[start.Col:end.Col]
	}

	var result string
	for row := start.Row; row <= end.Row; row++ {
		line := e.state.Buffer.Line(row)
		switch row {
		case start.Row:
			result += line[start.Col:]
		case end.Row:
			endCol := end.Col
			if endCol > len(line) {
				endCol = len(line)
			}
			result += "\n" + line[:endCol]
		default:
			result += "\n" + line
		}
	}
	return result
}
