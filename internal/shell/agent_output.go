package shell

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/agent"
)

// AgentOutputCoordinator draws a ?? turn's output: the spinner, the
// streamed reply, tool rows, the permission prompt and the confirmation
// hints. The turn's phase decides what is drawn when; the coordinator owns
// what the cursor's row holds (replyRow), so every status or error row
// starts on a row of its own (TakeRow). Its other switch is the hold the
// permission prompt puts on the reply: while a prompt is up, reply text and
// tool rows wait behind it and show once it clears.
//
// Other TUI components (ContextPicker, History search) are not managed here.
type AgentOutputCoordinator struct {
	mu            sync.Mutex
	out           io.Writer
	row           replyRow                    // what the cursor's row holds
	spinner       *spinnerRun                 // the spinner drawing the row, set only while row is rowSpinner
	spinnerRand   agentStatusRandom           // the spinner's motion
	agentModel    string                      // active model shown in spinner labels, "" hides it
	prompt        *permissionPrompt           // the prompt on screen, nil when there is none
	accentColorFn func() string               // Callback to get current accent color
	tools         map[string]toolDisplayState // this request's tools, by ACP tool ID
	toolSeq       int
}

// replyRow is what the cursor's row holds while a turn draws.
type replyRow int

const (
	rowFree     replyRow = iota // nothing yet: the next row starts here
	rowSpinner                  // the spinner, redrawn every frame
	rowMidReply                 // reply text that stopped partway through the row
)

// spinnerRun is one run of the spinner's goroutine: closing stop ends it.
// It draws text, under the coordinator's lock, only while it is the
// coordinator's spinner, so a stopped run never draws again.
type spinnerRun struct {
	stop chan struct{}
	text string
}

// permissionPrompt is a prompt on screen and what arrived behind it
// meanwhile.
type permissionPrompt struct {
	held strings.Builder // reply text and tool rows that arrived behind the prompt
	bar  string          // the accent bar its rows start with
}

type toolDisplayState struct {
	update    agent.ToolCallUpdate
	finalized bool
}

// NewAgentOutputCoordinator creates a new agent output coordinator.
func NewAgentOutputCoordinator(out io.Writer) *AgentOutputCoordinator {
	return &AgentOutputCoordinator{
		out:         out,
		spinnerRand: rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec // UI animation jitter does not require crypto randomness.
	}
}

// SetAccentColorFunc sets a callback to get the current accent color.
// This allows the color to be updated dynamically (e.g., after starship extraction).
func (aoc *AgentOutputCoordinator) SetAccentColorFunc(fn func() string) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.accentColorFn = fn
}

// BeginTurn starts a ?? turn's output on the fresh row the user's line left:
// no reply row of its own yet. A turn may fail before its first request (a
// pipe command that could not run), and must not end the row of the turn
// before.
func (aoc *AgentOutputCoordinator) BeginTurn() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	if aoc.row == rowSpinner {
		aoc.takeRowLocked()
	}
	aoc.row = rowFree
}

// SetAgentModel sets the model name shown in spinner labels. Pass "" to
// hide it.
func (aoc *AgentOutputCoordinator) SetAgentModel(name string) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.agentModel = name
}

// ShowState shows the agent's state on the spinner: a running spinner
// changes its text, else the spinner starts on a row of its own.
func (aoc *AgentOutputCoordinator) ShowState(state AgentState) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.showSpinnerLocked(agentStateLabel(state, aoc.agentModel))
}

// ShowActivity shows structured agent activity on the spinner, as
// ShowState does.
func (aoc *AgentOutputCoordinator) ShowActivity(label string) {
	if strings.TrimSpace(label) == "" {
		label = "agent · working"
	}
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.showSpinnerLocked(label)
}

func (aoc *AgentOutputCoordinator) showSpinnerLocked(text string) {
	if aoc.spinner != nil {
		aoc.spinner.text = text // its next frame draws it
		return
	}
	aoc.takeRowLocked()
	aoc.spinner = &spinnerRun{stop: make(chan struct{}), text: text}
	aoc.row = rowSpinner
	go aoc.runSpinner(aoc.spinner)
}

// runSpinner animates run until it is stopped.
func (aoc *AgentOutputCoordinator) runSpinner(run *spinnerRun) {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for frame := 0; ; frame++ {
		select {
		case <-run.stop:
			return
		case <-ticker.C:
		}
		aoc.mu.Lock()
		if aoc.spinner != run {
			aoc.mu.Unlock()
			return
		}
		motion := selectAgentStatusMotion(frame, aoc.spinnerRand)
		fmt.Fprintf(aoc.out, "\r\033[K%s", spinnerFrame(run.text, motion))
		aoc.mu.Unlock()
	}
}

// TakeRow frees the cursor's row for a status or error row: the spinner's
// row is erased, an unfinished reply row is ended, and a free row is left
// as it is. Every row that is not reply text starts here.
func (aoc *AgentOutputCoordinator) TakeRow() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.takeRowLocked()
}

// takeRowLocked is TakeRow, reporting what the row held. Must be called
// with mu held.
func (aoc *AgentOutputCoordinator) takeRowLocked() (was replyRow) {
	was = aoc.row
	switch was {
	case rowSpinner:
		close(aoc.spinner.stop)
		aoc.spinner = nil
		fmt.Fprint(aoc.out, "\r\033[K")
	case rowMidReply:
		fmt.Fprint(aoc.out, "\n")
	}
	aoc.row = rowFree
	return was
}

// BeginRequest starts a request's tools afresh: ACP tool IDs are only unique
// within one prompt, and a turn's retry is a new one.
func (aoc *AgentOutputCoordinator) BeginRequest() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.tools = make(map[string]toolDisplayState)
	aoc.toolSeq = 0
}

// RecordToolUpdate merges an ACP lifecycle update and reports whether it just
// reached a terminal state. Repeated terminal notifications are deduplicated.
func (aoc *AgentOutputCoordinator) RecordToolUpdate(next agent.ToolCallUpdate) (agent.ToolCallUpdate, bool) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	key := next.ID
	if key == "" {
		aoc.toolSeq++
		key = fmt.Sprintf("__tool_%d", aoc.toolSeq)
		next.ID = key
	}
	if aoc.tools == nil {
		aoc.tools = make(map[string]toolDisplayState)
	}
	state := aoc.tools[key]
	state.update = state.update.Merge(next)
	terminal := state.update.PermissionDenied || state.update.Status == agent.ToolCallCompleted || state.update.Status == agent.ToolCallFailed
	justFinalized := terminal && !state.finalized
	if justFinalized {
		state.finalized = true
	}
	aoc.tools[key] = state
	return state.update, justFinalized
}

// ResolveToolPermission associates a local allow/deny decision with its ACP
// lifecycle. A denial is terminal even when an agent omits a later update.
func (aoc *AgentOutputCoordinator) ResolveToolPermission(id string, allowed bool) (agent.ToolCallUpdate, bool) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	if aoc.tools == nil {
		aoc.tools = make(map[string]toolDisplayState)
	}
	key := id
	if key == "" {
		aoc.toolSeq++
		key = fmt.Sprintf("__tool_%d", aoc.toolSeq)
	}
	state := aoc.tools[key]
	if state.update.ID == "" {
		state.update.ID = key
	}
	if allowed || state.finalized {
		aoc.tools[key] = state
		return state.update, false
	}
	knownTool := state.update.Title != "" || state.update.Kind != "" || state.update.Status != ""
	state.update.PermissionDenied = true
	state.update.Status = agent.ToolCallFailed
	if knownTool {
		state.finalized = true
	}
	aoc.tools[key] = state
	return state.update, knownTool
}

// RenderToolResult writes the one durable lifecycle row for a tool. Tool
// content deliberately stays hidden; the agent's final response remains the
// authoritative explanation of what happened.
func (aoc *AgentOutputCoordinator) RenderToolResult(update agent.ToolCallUpdate, prefix string) {
	kind := sanitizeTerminalText(update.Kind)
	if kind == "" {
		kind = "tool"
	}
	title := sanitizeTerminalText(update.Title)
	if title == "" {
		title = "tool call"
	}
	title = truncateTerminalText(title, 96)

	icon, label, color := "✓", kind, "\x1b[32m"
	if update.PermissionDenied {
		icon, label, color = "–", "denied", "\x1b[33m"
	} else if update.Status != agent.ToolCallCompleted {
		// The red mark says it failed; the row still names the tool and,
		// when the agent sent one, why.
		icon, color = "✗", "\x1b[31m"
		if reason := toolFailureReason(update.Content); reason != "" {
			title += " · " + reason
		}
	}

	// One row: in a narrow pane the end is cut, a failure's reason first.
	if budget := rowBudget(terminalColumns()); budget > 0 {
		used := visibleCols(prefix) + len("  ✓ ") + runewidth.StringWidth(label) + len(" · ")
		title = fitText(title, max(budget-used, 1))
	}

	row := fmt.Sprintf("%s  %s%s\x1b[0m \x1b[90m%s · %s\x1b[0m\n", prefix, color, icon, label, title)
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	if aoc.prompt != nil {
		aoc.prompt.held.WriteString(row) // shown once the prompt clears
		return
	}
	aoc.takeRowLocked()
	fmt.Fprint(aoc.out, row)
}

var toolMarkupRe = regexp.MustCompile(`</?[a-z_]+>`)

// toolFailureReason is the first line of a failed tool's output, without
// the markup some agents wrap errors in, short enough for one row.
func toolFailureReason(content string) string {
	content = strings.TrimSpace(toolMarkupRe.ReplaceAllString(content, ""))
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		content = strings.TrimSpace(content[:i])
	}
	return truncateTerminalText(sanitizeTerminalText(content), 80)
}

// showTurnCanceled closes a canceled ?? turn with a row in the style of the
// tool rows above it, behind the same prefix (the conversation rail).
func showTurnCanceled(w io.Writer, prefix string) {
	fmt.Fprint(w, prefix+"  \x1b[33m–\x1b[0m \x1b[90mcanceled\x1b[0m\n")
}

func truncateTerminalText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// WriteStream draws reply text, or holds it behind a permission prompt.
func (aoc *AgentOutputCoordinator) WriteStream(text string) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	if aoc.prompt != nil {
		aoc.prompt.held.WriteString(text)
		return
	}
	aoc.drawStreamLocked(text)
}

// ANSI escape codes for permission prompt rendering.
const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiDim       = "\x1b[2m"
	ansiClearLine = "\x1b[2K"
	ansiCursorUp  = "\x1b[1A"
	permissionPad = "  "
)

// RenderPermissionPrompt draws the permission request and holds the reply
// behind it until it clears. toolName is optional: when set, it is shown as
// context (e.g., "Bash", "Write"). The prompt takes the cursor's row: one
// the spinner or an unfinished reply row held becomes the prompt's top
// blank line, so clearing the prompt hands the row back instead of leaving
// it behind in scrollback.
func (aoc *AgentOutputCoordinator) RenderPermissionPrompt(command, toolName, accentColor string) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	safeCommand := sanitizeTerminalText(command)
	safeToolName := sanitizeTerminalText(toolName)

	// One locked section puts the hold on and draws, so nothing streams in
	// between. A second prompt while one is up keeps what the first holds.
	takenRow := false // the cursor's row was the spinner's or the reply's: the prompt starts on it
	if aoc.prompt == nil {
		aoc.prompt = &permissionPrompt{}
		takenRow = aoc.takeRowLocked() != rowFree
	}

	// The prompt is erased as five rows, so none of them may wrap. The bar
	// and pad take four columns.
	width := terminalColumns()
	rowWidth := 0
	if budget := rowBudget(width); budget > 4 {
		rowWidth = budget - 4
	}
	safeCommand = fitText(safeCommand, rowWidth)
	if rowWidth > 0 {
		safeToolName = fitText(safeToolName, max(rowWidth-len("Agent wants to run ():"), 1))
	}
	keys := permissionKeysRow("", width)

	// Use callback for accent color if available, otherwise use passed-in color
	color := accentColor
	if aoc.accentColorFn != nil {
		if c := aoc.accentColorFn(); c != "" {
			color = c
		}
	}

	// Build accent color ANSI code
	accentCode := "\x1b[36m" // Default to cyan
	if color != "" && len(color) == 7 && color[0] == '#' {
		var r, g, b int
		if _, err := fmt.Sscanf(color[1:], "%02x%02x%02x", &r, &g, &b); err == nil {
			accentCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
		}
	}

	// Build the header line with optional tool name context
	header := ansiBold + "Agent wants to run:" + ansiReset
	if safeToolName != "" {
		header = ansiBold + "Agent wants to run " + ansiReset + "\x1b[90m(" + safeToolName + ")\x1b[0m" + ansiReset + ":"
	}

	// Render the prompt box with colored bar
	barStyle := accentCode + ansiBold
	aoc.prompt.bar = barStyle + "│" + ansiReset + " " + permissionPad
	lines := []string{
		"",
		barStyle + "│" + ansiReset + " " + permissionPad + header,
		barStyle + "│" + ansiReset + " " + permissionPad + accentCode + ansiBold + safeCommand + ansiReset,
		barStyle + "│" + ansiReset + " " + permissionPad,
		aoc.prompt.bar + keys,
	}

	var sb strings.Builder
	for i, line := range lines {
		if i == 0 && takenRow {
			sb.WriteString("\r")
		} else {
			sb.WriteString("\r\n")
		}
		sb.WriteString(ansiClearLine)
		sb.WriteString(line)
	}

	aoc.out.Write([]byte(sb.String()))
}

// permissionKeysRow is the prompt's last row, the keys that answer it, fit
// to width; a note, when set, comes first, dim, and stays when keys do not
// fit.
func permissionKeysRow(note string, width int) string {
	hints := []string{"y to allow", "n or esc to deny", "a to always allow", "ctrl+c to cancel"}
	if note == "" {
		return fitHint("", "", hints, max(width-4, 0))
	}
	row := fitHint("", "", append([]string{note}, hints...), max(width-4, 0))
	return ansiDim + note + ansiReset + strings.TrimPrefix(row, note)
}

// NoteTooQuick redraws the permission prompt's keys row with tooQuickNote
// first: a key came before the keys settled and was dropped. No-op once
// the prompt is gone. The cursor stays on that row, the prompt's last.
func (aoc *AgentOutputCoordinator) NoteTooQuick() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	if aoc.prompt == nil {
		return
	}
	row := "\r" + ansiClearLine + aoc.prompt.bar + permissionKeysRow(tooQuickNote, terminalColumns())
	aoc.out.Write([]byte(row))
	aoc.flushLocked()
}

func sanitizeTerminalText(text string) string {
	if text == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range text {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0x1b:
			b.WriteString(`\x1b`)
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&b, "\\u%04X", r)
				continue
			}
			b.WriteRune(r)
		}
	}

	return b.String()
}

// ClearPermissionPrompt takes an answered prompt off the screen and draws
// what it held. It adds no row of its own: the tool's lifecycle row says
// what happened. No-op once the prompt is gone (dismissed when its request
// ended): clearing rows then would erase unrelated output.
func (aoc *AgentOutputCoordinator) ClearPermissionPrompt() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	if aoc.prompt == nil {
		return
	}
	aoc.out.Write([]byte(clearPermissionRows))
	aoc.flushLocked() // the clear goes out before what follows
	held := aoc.prompt.held.String()
	aoc.prompt = nil
	aoc.row = rowFree // the cursor is on the prompt's top row, now blank
	aoc.drawStreamLocked(held)
}

// DismissPermissionPrompt takes a permission prompt off the screen when the
// request it asked for ended under it (a timeout, a cancel): its rows are
// cleared and what it held is dropped with the rest of the reply. The
// prompt's own clear, when its key read gives up later, is then a no-op.
func (aoc *AgentOutputCoordinator) DismissPermissionPrompt() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	if aoc.prompt == nil {
		return
	}
	aoc.out.Write([]byte(clearPermissionRows))
	aoc.prompt = nil
	aoc.row = rowFree // the cursor is on the prompt's top row, now blank
	aoc.flushLocked()
}

// clearPermissionRows erases the five rows of a permission prompt, the
// cursor on its last: that row, then four up.
var clearPermissionRows = "\r" + ansiClearLine + strings.Repeat(ansiCursorUp+"\r"+ansiClearLine, 4)

// flushLocked flushes the output if it supports syncing.
// Must be called with mu held.
func (aoc *AgentOutputCoordinator) flushLocked() {
	if f, ok := aoc.out.(*os.File); ok {
		f.Sync()
	}
}

// ShowHints displays confirmation keybindings.
func (aoc *AgentOutputCoordinator) ShowHints(ct ConfirmationType) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	// Fitted to the pane: the hint is erased as one row when the user acts.
	scope, hints := confirmationHints(ct)
	fmt.Fprintf(aoc.out, "\x1b[90m%s\x1b[0m\n", fitHint("  ", scope, hints, terminalColumns()))
}

// drawStreamLocked writes reply text, taking the row back from the
// spinner first, and remembers whether it left the row unfinished. Must be
// called with mu held.
func (aoc *AgentOutputCoordinator) drawStreamLocked(text string) {
	if text == "" {
		return
	}
	if aoc.row == rowSpinner {
		aoc.takeRowLocked()
	}
	fmt.Fprint(aoc.out, text)
	aoc.row = rowFree
	if !strings.HasSuffix(text, "\n") {
		aoc.row = rowMidReply
	}
}
