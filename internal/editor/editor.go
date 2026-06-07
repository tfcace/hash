// internal/editor/editor.go
package editor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/trace"
	"golang.design/x/clipboard"
	"golang.org/x/term"
)

// Completion represents a completion candidate.
type Completion struct {
	Text        string
	Description string
}

// completionDrillState stores parent directory context for drill-down navigation.
type completionDrillState struct {
	prefix string // The directory prefix that was drilled into
	filter string // The filter text that was active before drilling
	index  int    // The selected index before drilling
	col    int    // The completionCol value before drilling
}

// Config configures the editor.
type Config struct {
	Keybindings             string                                   // "helix", "emacs", "vim"
	HistoryFunc             func(dir int, currentLine string) string // -1=prev, +1=next; currentLine is for saving
	CompleteFunc            func(line string, pos int) []Completion  // Tab completion
	PrefetchFunc            func(line string, pos int)               // Background completion prefetch (on space)
	SuggestionFunc          func(input string) string                // Inline suggestion from history (Fish-style)
	OnInputReady            func()                                   // Called after editor chrome is rendered, before input loop
	Gutter                  bool                                     // Show gutter indicator
	Prompt                  string                                   // Prompt string to display before input
	InputBgColor            string                                   // Background color for submitted input (hex)
	ScrollbarColor          string                                   // Foreground color for scrollbars (hex)
	MaxPasteSize            uint                                     // Maximum paste size in bytes (default 10MB)
	DisableLineContinuation bool                                     // Disable shell-style line continuations on newline/paste
	InputFrame              *InputFrame                              // Optional frame for custom input rendering
	PreventEmptySubmit      bool                                     // Keep editor open when submitting an empty buffer
	DisableHistorySearch    bool                                     // Disable Ctrl+R history search
	DisableContextPicker    bool                                     // Disable Ctrl+P context picker
	ClearOnCancel           bool                                     // Clear display when Ctrl+C cancels input
	CancelOnEscape          bool                                     // Treat Escape as canceled input instead of mode/completion handling
}

// Result is returned when the editor exits.
type Result struct {
	Text          string
	Canceled      bool // Ctrl+C - interrupt
	EOF           bool // Ctrl+D - exit shell
	HistorySearch bool // Ctrl+R - launch history search
	ContextPicker bool // Ctrl+P - launch context picker
}

// GhostTextChan is a channel that receives ghost text updates.
type GhostTextChan <-chan string

// Editor is the main editor instance.
type Editor struct {
	config  Config
	input   *InputReader
	display *Display
	state   *EditorState
	mode    Mode

	in       io.Reader
	out      io.Writer
	oldState *term.State

	lastAction     Action
	lastActionTime time.Time

	clipboardInit bool // Lazy clipboard initialization

	// Completion state
	completionActive     bool
	completionItems      []Completion
	completionIndex      int                    // Selected item in menu
	completionPrefix     string                 // Text being completed (for replacement)
	completionCol        int                    // Column where completion started
	completionFilter     string                 // Live filter text while menu is open
	completionDrillStack []completionDrillState // Stack of parent dirs for backspace-up

	// Ghost text state (inline suggestions)
	ghost          *GhostText
	ghostTextChan  GhostTextChan // Channel for streaming ghost text updates
	ghostErrChan   <-chan error  // Channel for ghost text errors
	streamingModel string        // Model name for "Thinking..." display
}

// New creates a new editor.
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

	inputReader := NewInputReader(in)
	if cfg.MaxPasteSize > 0 {
		inputReader.SetMaxPasteSize(cfg.MaxPasteSize)
	}

	state.LineContinuation = !cfg.DisableLineContinuation
	state.AllowHistorySearch = !cfg.DisableHistorySearch
	state.AllowContextPicker = !cfg.DisableContextPicker

	return &Editor{
		config:  cfg,
		input:   inputReader,
		display: display,
		state:   state,
		mode:    mode,
		in:      in,
		out:     out,
		ghost:   NewGhostText(),
	}
}

// SetGhostText sets inline suggestion text that appears after the cursor.
func (e *Editor) SetGhostText(text string) {
	e.ghost.Set(text)
}

// SetGhostTextStreaming sets up streaming ghost text from channels.
// Text chunks arrive on textCh, errors on errCh.
func (e *Editor) SetGhostTextStreaming(textCh <-chan string, errCh <-chan error) {
	e.ghostTextChan = textCh
	e.ghostErrChan = errCh
	e.ghost.Clear()
	e.ghost.SetStreaming(true)
	e.ghost.FromAgent = true // Agent suggestions show hints
	// Dismiss any active completion menu - ghost text takes precedence
	e.dismissCompletion()
}

// SetStreamingModel sets the model name for "Thinking..." display.
func (e *Editor) SetStreamingModel(model string) {
	e.streamingModel = model
}

// ClearGhostText removes any ghost text.
func (e *Editor) ClearGhostText() {
	e.ghost.Clear()
	e.ghostTextChan = nil
	e.ghostErrChan = nil
}

// SetPromptWidth sets the prompt width for cursor positioning.
func (e *Editor) SetPromptWidth(cols int) {
	e.display.SetPromptWidth(cols)
}

// AnnotateDuration writes execution duration to the right side of a previous command line.
// Call after command execution. outputLines = lines of output, cmdLines = lines of command.
func AnnotateDuration(out io.Writer, termWidth, outputLines, cmdLines int, durationMs int64, bgColor string) {
	if durationMs < 100 { // Don't show for very fast commands
		return
	}

	// Format duration
	var text string
	switch {
	case durationMs < 1000:
		text = fmt.Sprintf(" %dms ", durationMs)
	case durationMs < 60000:
		text = fmt.Sprintf(" %.1fs ", float64(durationMs)/1000)
	default:
		mins := durationMs / 60000
		secs := (durationMs % 60000) / 1000
		text = fmt.Sprintf(" %dm%ds ", mins, secs)
	}

	linesToGoBack := outputLines + cmdLines
	if linesToGoBack <= 0 {
		return
	}

	var sb strings.Builder

	// Save cursor, move up, position at right edge
	sb.WriteString("\x1b[s")
	fmt.Fprintf(&sb, "\x1b[%dA", linesToGoBack)

	// Position at right side
	col := termWidth - len(text)
	if col > 0 {
		fmt.Fprintf(&sb, "\x1b[%dG", col)
	}

	// Print duration with dim styling
	sb.WriteString("\x1b[2m") // Dim
	if bgColor != "" {
		// Parse hex to RGB for background
		var r, g, b int
		if len(bgColor) == 7 && bgColor[0] == '#' {
			fmt.Sscanf(bgColor[1:], "%02x%02x%02x", &r, &g, &b) //nolint:errcheck // hex format already validated
			fmt.Fprintf(&sb, "\x1b[48;2;%d;%d;%dm", r, g, b)
		}
	}
	sb.WriteString(text)
	sb.WriteString("\x1b[0m")

	// Restore cursor
	sb.WriteString("\x1b[u")

	out.Write([]byte(sb.String()))
}

// SetInitialText sets the initial text in the editor buffer.
func (e *Editor) SetInitialText(text string) {
	e.state.Buffer = NewBufferFromString(text)
	// Move cursor to end of first line
	e.state.Cursor.MoveTo(0, len(e.state.Buffer.Line(0)))
}

// Run starts the editor and blocks until submit or cancel.
func (e *Editor) Run(ctx context.Context) (Result, error) {
	// Enable raw mode if we have a terminal
	cleanup := e.setupTerminalMode()
	if cleanup != nil {
		defer cleanup()
	}

	// Handle SIGWINCH for terminal resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// Push initial state for undo
	e.state.UndoStack.Push(e.state.Buffer, e.state.Cursor)

	// Configure display
	e.display.SetGutter(e.config.Gutter)
	e.display.SetPrompt(e.config.Prompt)

	// Initial render
	e.render()

	// Notify shell that input area is ready (for OSC 133;B)
	if e.config.OnInputReady != nil {
		e.config.OnInputReady()
	}

	// Start keyboard reader goroutine
	keyCh, keyErrCh, done := e.startKeyReader()
	defer func() {
		close(done)
		// Wait for the goroutine to fully exit so it stops reading stdin.
		// Without this, the goroutine may still be polling stdin when
		// bubbletea starts (e.g., for Ctrl+R history picker), causing a
		// race where two readers split terminal responses (DECRPM).
		for range keyCh {
		}
	}()

	return e.runEventLoop(ctx, sigCh, keyCh, keyErrCh)
}

// setupTerminalMode enables raw mode and enhanced keyboard protocols.
// Returns a cleanup function, or nil if not a terminal.
func (e *Editor) setupTerminalMode() func() {
	f, ok := e.in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}

	oldState, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return nil
	}
	e.oldState = oldState

	// Drain any input typed during the cooked-to-raw mode transition.
	e.input.DrainPending()

	// Enable enhanced keyboard protocol (CSI u mode) for Shift+Enter etc.
	e.out.Write([]byte("\x1b[>4;1u"))
	// Enable bracketed paste mode
	e.out.Write([]byte("\x1b[?2004h"))

	return func() {
		e.out.Write([]byte("\x1b[?2004l")) // Disable bracketed paste
		e.out.Write([]byte("\x1b[<u"))     // Disable enhanced keyboard
		term.Restore(int(f.Fd()), oldState)
	}
}

// startKeyReader starts a goroutine that reads keys and returns channels.
func (e *Editor) startKeyReader() (keyCh chan Key, errCh chan error, done chan struct{}) {
	keyCh = make(chan Key, 1)
	errCh = make(chan error, 1)
	done = make(chan struct{})

	go func() {
		defer close(keyCh)
		defer close(errCh)

		for {
			key, err := e.input.ReadKeyInterruptible(done)
			if err != nil {
				if err == context.Canceled {
					return
				}
				if err == io.EOF {
					return
				}
				select {
				case errCh <- err:
				case <-done:
				}
				return
			}
			select {
			case keyCh <- key:
			case <-done:
				return
			}
		}
	}()

	return keyCh, errCh, done
}

// runEventLoop is the main editor event loop.
func (e *Editor) runEventLoop(ctx context.Context, sigCh <-chan os.Signal, keyCh <-chan Key, keyErrCh <-chan error) (Result, error) {
	for {
		select {
		case <-ctx.Done():
			return Result{Canceled: true}, ctx.Err()
		case <-sigCh:
			e.handleResize()
		case text, ok := <-e.ghostTextChan:
			e.handleGhostTextUpdate(text, ok)
		case err, ok := <-e.ghostErrChan:
			e.handleGhostTextError(err, ok)
		case err, ok := <-keyErrCh:
			if !ok {
				keyErrCh = nil
				continue
			}
			return Result{}, err
		case key, ok := <-keyCh:
			if !ok {
				return Result{Text: e.state.Buffer.Content(), Canceled: true}, nil
			}
			if result, done := e.handleKeyEvent(key); done {
				return result, nil
			}
		}
	}
}

// handleGhostTextUpdate processes ghost text streaming updates.
func (e *Editor) handleGhostTextUpdate(text string, ok bool) {
	if !ok {
		e.ghost.SetStreaming(false)
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		e.render()
		return
	}
	e.ghost.Append(text)
	if e.completionActive {
		e.dismissCompletion()
	}
	e.render()
}

// handleGhostTextError processes ghost text errors.
func (e *Editor) handleGhostTextError(err error, ok bool) {
	if !ok {
		e.ghostErrChan = nil
		return
	}
	if err != nil {
		e.ghost.Clear()
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		e.render()
	}
}

// handleKeyEvent processes a key event. Returns (result, true) if the editor should exit.
func (e *Editor) handleKeyEvent(key Key) (Result, bool) {
	trace.EditorDetailed("key_dispatch", map[string]any{
		"key":               keyString(key),
		"ghost_active":      e.ghost.Active && !e.ghost.IsEmpty(),
		"ghost_streaming":   e.ghost.Streaming,
		"completion_active": e.completionActive,
		"mode":              e.mode.Name(),
	})

	// Handle Ctrl+C
	if key.Ctrl && key.Rune == 'c' {
		return e.handleCtrlC()
	}

	// Handle Ctrl+D - exit shell (EOF)
	if key.Ctrl && key.Rune == 'd' {
		if e.state.Buffer.Content() == "" {
			return Result{EOF: true}, true
		}
		return Result{}, false
	}

	if e.config.CancelOnEscape && key.Special == KeyEscape {
		e.dismissCompletion()
		e.ghost.Clear()
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		return Result{Canceled: true}, true
	}

	// Handle ghost text interception
	if e.ghost.Active && (!e.ghost.IsEmpty() || e.ghost.Streaming) {
		if e.handleGhostTextKey(key) {
			e.render()
			return Result{}, false
		}
		e.ghost.Clear()
		e.dismissCompletion()
	}

	// Handle completion menu interception
	if e.completionActive && e.handleCompletionKey(key) {
		e.render()
		return Result{}, false
	}

	// Delegate to mode and process result
	modeResult := e.mode.HandleKey(key, e.state)
	if result, done := e.processModeResult(modeResult); done {
		return result, true
	}

	e.state.Cursor.Clamp(e.state.Buffer)
	e.render()
	return Result{}, false
}

// handleCtrlC handles Ctrl+C key press.
func (e *Editor) handleCtrlC() (Result, bool) {
	e.dismissCompletion()
	if e.ghost.Streaming || e.ghostTextChan != nil {
		e.ghost.Clear()
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		return Result{Canceled: true}, true
	}
	e.ghost.Clear()
	if e.state.Buffer.Content() == "" {
		if e.config.ClearOnCancel {
			e.display.Clear()
		}
		return Result{Canceled: true}, true
	}
	e.state.Buffer = NewBuffer()
	e.state.Cursor = NewCursor()
	e.render()
	return Result{}, false
}

// processModeResult handles the result from mode.HandleKey.
// Returns (result, true) if the editor should exit.
func (e *Editor) processModeResult(result ModeResult) (Result, bool) {
	if result.NewMode != nil {
		e.mode = result.NewMode
	}
	e.handleAction(result.Action)

	if result.Submit {
		if e.config.PreventEmptySubmit && e.state.Buffer.Content() == "" {
			e.render()
			return Result{}, false
		}
		e.display.Finalize(e.state.Buffer)
		return Result{Text: e.state.Buffer.Content()}, true
	}
	if result.HistorySearch {
		e.display.Clear()
		return Result{Text: e.state.Buffer.Content(), HistorySearch: true}, true
	}
	if result.ContextPicker {
		e.display.Clear()
		return Result{Text: e.state.Buffer.Content(), ContextPicker: true}, true
	}

	e.handleHistoryNavigation(result)
	e.handleCompletionAndClipboard(result)

	// Update inline suggestion after buffer changes
	if result.Action == ActionInsert || result.Action == ActionDelete || result.Action == ActionPaste {
		e.updateSuggestion()
	}

	return Result{}, false
}

// handleHistoryNavigation processes history prev/next from mode result.
func (e *Editor) handleHistoryNavigation(result ModeResult) {
	if e.config.HistoryFunc == nil {
		return
	}
	if result.HistoryPrev {
		if entry := e.config.HistoryFunc(-1, e.state.Buffer.Content()); entry != "" {
			e.state.Buffer = NewBufferFromString(entry)
			e.state.Cursor.MoveTo(0, len(e.state.Buffer.Line(0)))
		}
	}
	if result.HistoryNext {
		if entry := e.config.HistoryFunc(1, e.state.Buffer.Content()); entry != "" {
			e.state.Buffer = NewBufferFromString(entry)
			e.state.Cursor.MoveTo(0, len(e.state.Buffer.Line(0)))
		}
	}
}

// handleCompletionAndClipboard processes completion and clipboard operations.
func (e *Editor) handleCompletionAndClipboard(result ModeResult) {
	if result.Complete && !e.ghost.Streaming {
		e.triggerCompletion()
	}
	if result.Prefetch {
		e.triggerPrefetch()
	}
	if result.Yank {
		e.yank()
	}
	if result.Paste {
		e.handleAction(ActionPaste)
		e.paste(false)
	}
	if result.PasteBefore {
		e.handleAction(ActionPaste)
		e.paste(true)
	}
}

// updateSuggestion queries the SuggestionFunc and sets ghost text for inline suggestions.
func (e *Editor) updateSuggestion() {
	if e.config.SuggestionFunc == nil {
		return
	}

	// Don't overwrite agent ghost text
	if e.ghost.FromAgent {
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
			e.ghost.FromAgent = false
			return
		}
	}

	// No match — clear non-agent ghost
	if !e.ghost.FromAgent {
		e.ghost.Clear()
	}
}

func (e *Editor) render() {
	hasSelection := e.state.Cursor.HasSelection()
	e.display.SetMode(e.mode.Name())

	// Pass ghost text to display for inline rendering
	ghostText := ""
	ghostStreaming := e.ghost.Streaming
	ghostFromAgent := e.ghost.FromAgent
	if e.ghost.Active && !e.ghost.IsEmpty() {
		ghostText = e.ghost.Remaining()
	}
	e.display.RenderWithGhost(e.state.Buffer, e.state.Cursor, hasSelection, ghostText, ghostStreaming, ghostFromAgent, e.streamingModel)

	// Render completion menu if active
	if e.completionActive {
		filtered := e.filteredCompletionItems()
		if len(filtered) > 0 {
			displayItems := make([]CompletionItem, len(filtered))
			for i, item := range filtered {
				displayItems[i] = CompletionItem(item)
			}
			e.display.RenderCompletionMenu(
				displayItems,
				e.completionIndex,
				e.completionCol,
				e.state.Cursor.Pos.Row,
				e.state.Cursor.Pos.Col,
			)
		}
	}
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
		if !e.ghost.FromAgent {
			// For history predictions: Tab dismisses ghost and triggers completion instead.
			// Use Right arrow to accept predictions (fish-style).
			e.ghost.Clear()
			e.ghostTextChan = nil
			e.ghostErrChan = nil
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
		// Stop any active streaming
		e.ghostTextChan = nil
		e.ghostErrChan = nil
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
		// Stop any active streaming
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		return true

	case KeyEscape:
		// Escape dismisses ghost text and stops streaming
		trace.AgentHigh("ghost_accept", map[string]any{
			"key":    "Escape",
			"action": "dismiss",
		})
		e.ghost.Clear()
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		return true

	case KeyEnter:
		// Enter dismisses ghost and submits what's typed (fish-style)
		trace.AgentHigh("ghost_accept", map[string]any{
			"key":    "Enter",
			"action": "dismiss_and_submit",
		})
		e.ghost.Clear()
		e.ghostTextChan = nil
		e.ghostErrChan = nil
		// Don't return true - let Enter propagate to submit
		return false
	}

	// Any other key clears ghost text (handled by caller)
	trace.AgentDetailed("accept_blocked", map[string]any{
		"key":    keyString(key),
		"reason": "unhandled_key",
		"state":  "ghost_active",
	})
	return false
}

// insertText inserts text at the cursor position.
func (e *Editor) insertText(text string) {
	row, col := e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col
	e.state.Buffer.Insert(row, col, text)

	// Move cursor to end of inserted text
	for _, r := range text {
		if r == '\n' {
			row++
			col = 0
		} else {
			col += len(string(r))
		}
	}
	e.state.Cursor.Pos.Row = row
	e.state.Cursor.Pos.Col = col
}

func (e *Editor) handleResize() {
	if f, ok := e.out.(*os.File); ok {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil {
			e.display.Resize(w, h)
		}
	}
	e.render()
}

func (e *Editor) handleAction(action Action) {
	if action == ActionNone {
		return
	}

	now := time.Now()

	// Push to undo stack if:
	// - Action type changed
	// - It's been more than 300ms since last action
	// - It's a mode change, paste, or explicit undo point
	shouldPush := action != e.lastAction ||
		now.Sub(e.lastActionTime) > 300*time.Millisecond ||
		action == ActionModeChange ||
		action == ActionPaste

	if shouldPush {
		e.state.UndoStack.Push(e.state.Buffer, e.state.Cursor)
	}

	e.lastAction = action
	e.lastActionTime = now
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

	start, end := e.state.Cursor.SelectionRange()
	text := e.extractText(start, end)
	clipboard.Write(clipboard.FmtText, []byte(text))

	// Clear selection after yank (Helix behavior)
	e.state.Cursor.ClearSelection()
}

// paste inserts text from the system clipboard at the cursor position.
func (e *Editor) paste(before bool) {
	e.initClipboard()

	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		return
	}

	text := string(data)
	row, col := e.state.Cursor.Pos.Row, e.state.Cursor.Pos.Col

	if !before {
		// Move past current character for 'p' (paste after)
		line := e.state.Buffer.Line(row)
		if col < len(line) {
			col = nextRuneBoundary(line, col)
		}
	}

	e.state.Buffer.Insert(row, col, text)

	// Move cursor to end of pasted text
	lines := 0
	lastLineLen := 0
	for i, r := range text {
		if r == '\n' {
			lines++
			lastLineLen = 0
		} else {
			lastLineLen = len(text) - i
			for j := i; j < len(text); j++ {
				if text[j] == '\n' {
					lastLineLen = j - i
					break
				}
			}
		}
	}

	if lines > 0 {
		e.state.Cursor.Pos.Row = row + lines
		e.state.Cursor.Pos.Col = lastLineLen
	} else {
		e.state.Cursor.Pos.Col = col + len(text)
	}
}

// triggerCompletion fetches completions and activates the menu if needed.
func (e *Editor) triggerCompletion() {
	if e.config.CompleteFunc == nil {
		return
	}

	line := e.state.Buffer.Content()
	pos := e.cursorOffset()

	items := e.config.CompleteFunc(line, pos)
	if len(items) == 0 {
		e.completionActive = false
		return
	}

	// Find the word being completed for replacement
	// Use current line only (not full buffer content) for prefix extraction
	currentLine := e.state.Buffer.Line(e.state.Cursor.Pos.Row)
	e.completionCol = e.findWordStart()
	e.completionPrefix = currentLine[e.completionCol:e.state.Cursor.Pos.Col]

	if len(items) == 1 && !strings.HasSuffix(items[0].Text, "/") {
		// Single non-directory match: insert inline immediately
		e.acceptCompletion(items[0])
		return
	}

	// Multiple matches or single directory: show menu
	e.completionItems = items
	e.completionIndex = 0
	e.completionFilter = ""
	e.completionActive = true
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

// findWordStart returns the column where the current word starts.
func (e *Editor) findWordStart() int {
	line := e.state.Buffer.Line(e.state.Cursor.Pos.Row)
	pos := e.state.Cursor.Pos.Col
	if pos > len(line) {
		pos = len(line)
	}

	start := 0
	scanner := shellWordScanner{}

	for i := 0; i < pos; {
		r, size := utf8.DecodeRuneInString(line[i:pos])
		if r == utf8.RuneError && size == 0 {
			break
		}

		if scanner.consume(r) {
			start = i + size
		}

		i += size
	}

	return start
}

type shellWordScanner struct {
	inSingle bool
	inDouble bool
	escaped  bool
}

func (s *shellWordScanner) consume(r rune) bool {
	if s.escaped {
		s.escaped = false
		return false
	}
	if r == '\\' && !s.inSingle {
		s.escaped = true
		return false
	}
	if r == '\'' && !s.inDouble {
		s.inSingle = !s.inSingle
		return false
	}
	if r == '"' && !s.inSingle {
		s.inDouble = !s.inDouble
		return false
	}
	return (r == ' ' || r == '\t') && !s.inSingle && !s.inDouble
}

// acceptCompletion replaces the current word with the selected completion.
func (e *Editor) acceptCompletion(item Completion) {
	row := e.state.Cursor.Pos.Row
	endCol := e.state.Cursor.Pos.Col

	// Delete from word start to cursor
	e.state.Buffer.Delete(Position{row, e.completionCol}, Position{row, endCol})

	// Insert completion text
	e.state.Buffer.Insert(row, e.completionCol, item.Text)

	// Move cursor to end of inserted text
	e.state.Cursor.Pos.Col = e.completionCol + len(item.Text)

	// Dismiss completion
	e.dismissCompletion()
}

// drillIntoDirectory replaces the current word with the directory path
// and re-triggers completion to show its contents.
func (e *Editor) drillIntoDirectory(item Completion) {
	// Push current state onto drill stack
	e.completionDrillStack = append(e.completionDrillStack, completionDrillState{
		prefix: e.completionPrefix,
		filter: e.completionFilter,
		index:  e.completionIndex,
		col:    e.completionCol,
	})

	// Replace the current word with the directory path
	row := e.state.Cursor.Pos.Row
	endCol := e.state.Cursor.Pos.Col
	e.state.Buffer.Delete(Position{row, e.completionCol}, Position{row, endCol})
	e.state.Buffer.Insert(row, e.completionCol, item.Text)
	e.state.Cursor.Pos.Col = e.completionCol + len(item.Text)

	// Reset filter
	e.completionFilter = ""
	e.completionIndex = 0

	// Re-query completions for the new path
	if e.config.CompleteFunc != nil {
		line := e.state.Buffer.Content()
		pos := e.cursorOffset()
		items := e.config.CompleteFunc(line, pos)
		if len(items) == 0 {
			// Empty directory — accept as-is
			e.completionDrillStack = e.completionDrillStack[:len(e.completionDrillStack)-1]
			e.dismissCompletion()
			return
		}

		e.completionItems = items
		e.completionPrefix = item.Text
		e.completionCol = e.findWordStart()

		// Auto-drill single-child directories (with depth limit to prevent stack overflow)
		if len(items) == 1 && strings.HasSuffix(items[0].Text, "/") && len(e.completionDrillStack) <= 20 {
			e.drillIntoDirectory(items[0])
			return
		}
	}
}

// drillUp pops the drill stack and restores the parent directory state.
func (e *Editor) drillUp() {
	if len(e.completionDrillStack) == 0 {
		return
	}

	// Pop the last state
	prev := e.completionDrillStack[len(e.completionDrillStack)-1]
	e.completionDrillStack = e.completionDrillStack[:len(e.completionDrillStack)-1]

	// Remove the drilled-into segment from the buffer.
	// Delete from the parent's completionCol to the current cursor position,
	// which removes everything that was inserted during the drill.
	row := e.state.Cursor.Pos.Row
	endCol := e.state.Cursor.Pos.Col
	e.state.Buffer.Delete(Position{row, prev.col}, Position{row, endCol})
	e.state.Cursor.Pos.Col = prev.col

	// Restore completionCol from the stack
	e.completionCol = prev.col

	// Re-query completions for the parent
	if e.config.CompleteFunc != nil {
		line := e.state.Buffer.Content()
		pos := e.cursorOffset()
		items := e.config.CompleteFunc(line, pos)
		e.completionItems = items
	}

	// Restore filter, index, and prefix
	e.completionFilter = prev.filter
	e.completionIndex = prev.index
	if len(e.completionItems) > 0 && e.completionIndex >= len(e.completionItems) {
		e.completionIndex = 0
	}
	e.completionPrefix = prev.prefix
}

// dismissCompletion closes the completion menu.
func (e *Editor) dismissCompletion() {
	if e.completionActive {
		e.display.ClearCompletionMenu(
			len(e.filteredCompletionItems()),
			e.state.Cursor.Pos.Row,
			e.state.Cursor.Pos.Col,
		)
	}
	e.completionActive = false
	e.completionItems = nil
	e.completionIndex = 0
	e.completionFilter = ""
	e.completionDrillStack = nil
}

// filterCompletionItems filters completion items by a case-insensitive prefix match.
func filterCompletionItems(items []Completion, filter string) []Completion {
	if filter == "" {
		return items
	}
	filter = strings.ToLower(filter)
	var result []Completion
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item.Text), filter) {
			result = append(result, item)
		}
	}
	return result
}

// filteredCompletionItems returns the completion items filtered by the current filter text.
func (e *Editor) filteredCompletionItems() []Completion {
	return filterCompletionItems(e.completionItems, e.completionFilter)
}

// handleCompletionKey processes keys when completion menu is active.
// Returns true if the key was handled, false to pass through.
//
//nolint:gocyclo // key dispatch switch is inherently branchy but straightforward
func (e *Editor) handleCompletionKey(key Key) bool {
	if len(e.completionItems) == 0 {
		e.dismissCompletion()
		return false
	}

	filtered := e.filteredCompletionItems()

	switch key.Special {
	case KeyDown:
		if len(filtered) > 0 {
			e.completionIndex = (e.completionIndex + 1) % len(filtered)
		}
		return true

	case KeyUp:
		if len(filtered) > 0 {
			e.completionIndex--
			if e.completionIndex < 0 {
				e.completionIndex = len(filtered) - 1
			}
		}
		return true

	case KeyTab:
		// Tab cycles to next item (like Down)
		if len(filtered) > 0 {
			e.completionIndex = (e.completionIndex + 1) % len(filtered)
		}
		return true

	case KeyEnter:
		// Enter always accepts the selected item and closes the menu
		if len(filtered) > 0 {
			e.acceptCompletion(filtered[e.completionIndex])
		} else {
			e.dismissCompletion()
		}
		return true

	case KeyRight:
		if len(filtered) > 0 {
			selected := filtered[e.completionIndex]
			if strings.HasSuffix(selected.Text, "/") {
				e.drillIntoDirectory(selected)
			} else {
				e.acceptCompletion(selected)
			}
		} else {
			e.dismissCompletion()
		}
		return true

	case KeyEscape:
		e.dismissCompletion()
		return true

	case KeyBackspace:
		switch {
		case e.completionFilter != "":
			// Remove last character from filter
			e.completionFilter = e.completionFilter[:len(e.completionFilter)-1]
			e.completionIndex = 0
		case len(e.completionDrillStack) > 0:
			e.drillUp()
		default:
			// No filter, no drill stack — dismiss
			e.dismissCompletion()
			return false
		}
		return true

	default:
		// Printable characters should continue editing the command. Dismiss the
		// menu and let the active mode handle the key.
		if key.Special == 0 && key.Rune >= 32 && !key.Ctrl && !key.Alt {
			e.dismissCompletion()
			return false
		}

		// Non-printable: dismiss and pass through
		e.dismissCompletion()
		return false
	}
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
