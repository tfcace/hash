package shell

import (
	"fmt"
	"io"
	"strings"

	"github.com/tfcace/hash/internal/termtext"
)

// The spinner is four cells in which noise resolves, left to right, into the
// shell's name, holds, dissolves, and starts over.
const (
	spinnerName = "hash"

	spinnerFirstLetter = 6  // frames of pure noise before the first letter settles
	spinnerLetterStep  = 5  // frames between one letter settling and the next
	spinnerDissolveAt  = 38 // frame the settled name starts to dissolve
	spinnerCycle       = 46 // frames in one whole cycle, about 3.7s at 80ms a frame
)

// spinnerNoise is what a cell shows before its letter settles. No glyph is a
// letter of the name, so noise never reads as a settled cell.
var spinnerNoise = []string{"░", "▒", "▓", "█", "-", "\\", "|", "/", "+", "*", "$", "#"}

// spinnerDissolve is what every cell shows, two frames each, as the name
// fades before the next cycle.
var spinnerDissolve = []string{"▓", "▒", "░", " "}

// spinnerCell is one column of the spinner: settled once it shows its letter.
type spinnerCell struct {
	glyph   string
	settled bool
}

type agentStatusRandom interface {
	Intn(n int) int
}

// ConfirmAction represents the user's response to a command suggestion.
type ConfirmAction int

const (
	ConfirmRun    ConfirmAction = iota // User pressed Enter - run the command
	ConfirmEdit                        // User pressed Tab - edit the command
	ConfirmCancel                      // User pressed Esc - cancel
	ConfirmReply                       // User pressed r - continue the conversation
	ConfirmEnded                       // The shell ended (a fatal signal) before a key answered: nothing more is drawn or recorded
)

// ConfirmationType determines which confirmation options to show.
type ConfirmationType int

const (
	ConfirmTypeCommand     ConfirmationType = iota // enter to run · tab to edit · esc to cancel
	ConfirmTypeExplanation                         // enter to finish · tab to copy · r to reply
	ConfirmTypeError                               // enter to retry · esc to cancel
)

// AgentState represents the current state of an agent request.
type AgentState int

const (
	AgentStateConnecting AgentState = iota
	AgentStateSending
	AgentStateThinking
	AgentStateReceiving
)

func (s AgentState) String() string {
	switch s {
	case AgentStateConnecting:
		return "agent · connecting"
	case AgentStateSending:
		return "agent · sending context"
	case AgentStateThinking:
		return "agent · thinking"
	case AgentStateReceiving:
		return "agent · receiving"
	default:
		return "agent · processing"
	}
}

// agentStateLabel renders the spinner label, appending the active model after a
// "·" separator (e.g. "agent · thinking · Sonnet"). An empty model omits it.
//
// We use "·" rather than wrapping the name in [ ] because model names can
// themselves contain brackets (the agent advertises values like "sonnet[1m]"
// and names like "Sonnet (1M context)"), which would otherwise nest awkwardly.
func agentStateLabel(state AgentState, model string) string {
	label := state.String()
	if m := cleanModelLabel(model); m != "" {
		label += " · " + m
	}
	return label
}

// cleanModelLabel tidies an agent-advertised model name for display: it trims
// surrounding whitespace, drops the trailing " (recommended)" tag the agent
// appends to its default model, and draws the rest inert.
func cleanModelLabel(model string) string {
	model = strings.TrimSpace(model)
	const tag = " (recommended)"
	if len(model) >= len(tag) && strings.EqualFold(model[len(model)-len(tag):], tag) {
		model = strings.TrimSpace(model[:len(model)-len(tag)])
	}
	return termtext.Inert(model)
}

// ResponseUI handles displaying agent responses.
type ResponseUI struct {
	out      io.Writer
	progress *progressBar // the OSC 9;4 indicator: the ?? turn and model update own it, not the spinner
}

// NewResponseUI creates a new response UI.
func NewResponseUI(out io.Writer) *ResponseUI {
	return &ResponseUI{
		out:      out,
		progress: newProgressBar(out),
	}
}

// spinnerCells is the spinner at a frame. Noise is picked by rng, or by the
// frame when there is none.
func spinnerCells(frame int, rng agentStatusRandom) []spinnerCell {
	at := frame % spinnerCycle
	cells := make([]spinnerCell, 0, len(spinnerName))
	for i, letter := range spinnerName {
		switch {
		case at >= spinnerDissolveAt:
			step := min((at-spinnerDissolveAt)/2, len(spinnerDissolve)-1)
			cells = append(cells, spinnerCell{glyph: spinnerDissolve[step]})
		case at >= spinnerFirstLetter+i*spinnerLetterStep:
			cells = append(cells, spinnerCell{glyph: string(letter), settled: true})
		case rng != nil:
			cells = append(cells, spinnerCell{glyph: spinnerNoise[rng.Intn(len(spinnerNoise))]})
		default:
			cells = append(cells, spinnerCell{glyph: spinnerNoise[(frame*7+i*5)%len(spinnerNoise)]})
		}
	}
	return cells
}

// spinnerFrame is one spinner frame fitted to the pane. The frame is
// redrawn with \r every tick; one that wraps leaves a row behind each time.
func spinnerFrame(text string, cells []spinnerCell, accent string) string {
	if budget := rowBudget(terminalColumns()); budget > 0 {
		text = fitText(text, budget-len(cells)-2) // the leading space, the cells, a space
	}
	return formatSpinner(text, cells, accent)
}

// formatSpinner draws the cells and the label: settled letters in the bold
// accent, noise in the same accent drawn faint, the label gray. An accent the terminal
// cannot be given (none extracted yet) falls back to cyan.
func formatSpinner(text string, cells []spinnerCell, accent string) string {
	color := fgCode(accent)
	if color == "" {
		color = fgCode("6")
	}
	var b strings.Builder
	b.WriteString(" ")
	for _, c := range cells {
		if c.settled {
			b.WriteString(ansiBold + color + c.glyph + ansiReset)
		} else {
			b.WriteString(ansiFaint + color + c.glyph + ansiReset)
		}
	}
	b.WriteString(" \033[90m" + text + "\033[0m")
	return b.String()
}

// StartProgress starts the OSC 9;4 progress bar without showing text.
func (u *ResponseUI) StartProgress() {
	u.progress.start()
}

// StopProgress stops the OSC 9;4 progress bar.
func (u *ResponseUI) StopProgress() {
	u.progress.done()
}

// ClearLines moves cursor up n lines and clears each one.
// Used to remove streamed response when user cancels.
func (u *ResponseUI) ClearLines(n int) {
	for i := 0; i < n; i++ {
		fmt.Fprintf(u.out, "\033[A\033[K") // Move up, clear line
	}
}

// ShowError displays an error message in the response area. The message
// may be the agent's (an RPC error), so each of its lines is drawn inert.
func (u *ResponseUI) ShowError(errMsg string) {
	lines := strings.Split(strings.ReplaceAll(errMsg, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = termtext.Inert(line)
	}
	fmt.Fprintf(u.out, "\033[31m✗ %s\033[0m\n", strings.Join(lines, "\n"))
}

const claudeAgentACPInstallCommand = "npm install -g @agentclientprotocol/claude-agent-acp"

// ShowAgentHint displays troubleshooting hints for agent connection failures.
func (u *ResponseUI) ShowAgentHint(transport, command, url string) {
	fmt.Fprintln(u.out)
	fmt.Fprintf(u.out, "\033[90m  Troubleshooting:\033[0m\n")

	if transport == "http" {
		fmt.Fprintf(u.out, "\033[90m  • Is the service running?\033[0m\n")
		fmt.Fprintf(u.out, "\033[90m  • Is it listening on the configured URL? (%s)\033[0m\n", url)
		fmt.Fprintf(u.out, "\033[90m  • Check firewall or network settings\033[0m\n")
	} else {
		// stdio transport (default)
		fmt.Fprintf(u.out, "\033[90m  • Is '%s' installed?\033[0m\n", command)
		if command == "claude-agent-acp" {
			fmt.Fprintf(u.out, "\033[90m  • Install it with: %s\033[0m\n", claudeAgentACPInstallCommand)
		}
		fmt.Fprintf(u.out, "\033[90m  • Is it in your PATH? (try: which %s)\033[0m\n", command)
		fmt.Fprintf(u.out, "\033[90m  • Is it executable? (try: ls -la $(which %s))\033[0m\n", command)
	}
	fmt.Fprintln(u.out)
}

// ShowModelHint follows an agent error about the session's model with the
// way out: the model builtin, which lists what this agent offers and switches.
func (u *ResponseUI) ShowModelHint() {
	fmt.Fprintln(u.out)
	fmt.Fprintf(u.out, "\033[90m  Pick a model this agent can use: run `model --list` to see the choices, then `model <name>`.\033[0m\n")
	fmt.Fprintln(u.out)
}

// confirmationHints are the scope label and keys of a confirmation footer.
func confirmationHints(ct ConfirmationType) (scope string, hints []string) {
	switch ct {
	case ConfirmTypeCommand:
		return "cmd", []string{"enter to run", "tab to edit", "esc to cancel"}
	case ConfirmTypeExplanation:
		return "agent", []string{"enter to finish", "tab to copy", "r to reply"}
	case ConfirmTypeError:
		return "error", []string{"enter to retry", "esc to cancel"}
	}
	return "", nil
}
