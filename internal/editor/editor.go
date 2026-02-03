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

	"github.com/tfcace/hash/internal/trace"
	"golang.design/x/clipboard"
	"golang.org/x/term"
)

// Completion represents a completion candidate.
type Completion struct {
	Text        string
	Description string
}

// Config configures the editor.
type Config struct {
	Keybindings    string                                   // "helix", "emacs", "vim"
	HistoryFunc    func(dir int, currentLine string) string // -1=prev, +1=next; currentLine is for saving
	CompleteFunc   func(line string, pos int) []Completion  // Tab completion
	PrefetchFunc   func(line string, pos int)               // Background completion prefetch (on space)
	OnInputReady   func()                                   // Called after editor chrome is rendered, before input loop
	Gutter         bool                                     // Show gutter indicator
	Prompt         string                                   // Prompt string to display before input
	InputBgColor   string                                   // Background color for submitted input (hex)
	ScrollbarColor string                                   // Foreground color for scrollbars (hex)
	MaxPasteSize   uint                                     // Maximum paste size in bytes (default 10MB)
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
	completionActive bool
	completionItems  []Completion
	completionIndex  int    // Selected item in menu
	completionPrefix string // Text being completed (for replacement)
	completionCol    int    // Column where completion started

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

	inputReader := NewInputReader(in)
	if cfg.MaxPasteSize > 0 {
		inputReader.SetMaxPasteSize(cfg.MaxPasteSize)
	}

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
	defer close(done)

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
		for {
			key, err := e.input.ReadKeyInterruptible(done)
			if err != nil {
				if err == context.Canceled {
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
		case err := <-keyErrCh:
			if err == io.EOF {
				return Result{Text: e.state.Buffer.Content(), Canceled: true}, nil
			}
			return Result{}, err
		case key := <-keyCh:
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
	if e.completionActive && len(e.completionItems) > 0 {
		displayItems := make([]CompletionItem, len(e.completionItems))
		for i, item := range e.completionItems {
			displayItems[i] = CompletionItem(item)
		}
		e.display.RenderCompletionMenu(displayItems, e.completionIndex, e.completionCol)
	}
}

// handleGhostTextKey processes keys when ghost text is active.
// Returns true if the key was handled.
func (e *Editor) handleGhostTextKey(key Key) bool {
	switch key.Special {
	case KeyTab:
		// Tab accepts the full ghost text and stops streaming
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
		// Right arrow accepts one character
		text := e.ghost.AcceptChar()
		trace.Agent("ghost_accept", map[string]any{
			"key":      "Right",
			"accepted": text,
			"action":   "accept_char",
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

	// Alt+Tab or Ctrl+Tab could accept word-by-word
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
			col++
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
		lineLen := len(e.state.Buffer.Line(row))
		if col < lineLen {
			col++
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

	if len(items) == 1 {
		// Single match: insert inline immediately
		e.acceptCompletion(items[0])
		return
	}

	// Multiple matches: show menu
	e.completionItems = items
	e.completionIndex = 0
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
	col := e.state.Cursor.Pos.Col
	for col > 0 && col <= len(line) && line[col-1] != ' ' && line[col-1] != '\t' {
		col--
	}
	return col
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

// dismissCompletion closes the completion menu.
func (e *Editor) dismissCompletion() {
	if e.completionActive {
		e.display.ClearCompletionMenu(len(e.completionItems))
	}
	e.completionActive = false
	e.completionItems = nil
	e.completionIndex = 0
}

// handleCompletionKey processes keys when completion menu is active.
// Returns true if the key was handled, false to pass through.
func (e *Editor) handleCompletionKey(key Key) bool {
	if len(e.completionItems) == 0 {
		e.dismissCompletion()
		return false
	}

	switch key.Special {
	case KeyDown:
		e.completionIndex = (e.completionIndex + 1) % len(e.completionItems)
		return true

	case KeyUp:
		e.completionIndex--
		if e.completionIndex < 0 {
			e.completionIndex = len(e.completionItems) - 1
		}
		return true

	case KeyTab:
		// Tab moves down in completion menu
		e.completionIndex = (e.completionIndex + 1) % len(e.completionItems)
		return true

	case KeyEnter:
		// Accept selected completion
		e.acceptCompletion(e.completionItems[e.completionIndex])
		return true

	case KeyEscape:
		// Dismiss menu
		e.dismissCompletion()
		return true
	}

	// Any other key dismisses completion and passes through
	e.dismissCompletion()
	return false
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
