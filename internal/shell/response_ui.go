package shell

import (
	"fmt"
	"io"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/termtext"
)

var agentStatusReplacementPool = []string{
	"░",
	"▒",
	"▓",
	"█",
	"-",
	"\\",
	"|",
	"/",
	"+",
	"*",
	"h",
	"a",
	"s",
	"h",
	"$",
	"#",
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

func selectAgentStatusMotion(frame int, rng agentStatusRandom) string {
	if len(agentStatusReplacementPool) == 0 {
		return ""
	}
	if rng != nil {
		return agentStatusReplacementPool[rng.Intn(len(agentStatusReplacementPool))]
	}
	return agentStatusReplacementPool[frame%len(agentStatusReplacementPool)]
}

// spinnerFrame is one spinner frame fitted to the pane. The frame is
// redrawn with \r every tick; one that wraps leaves a row behind each time.
func spinnerFrame(text, motion string) string {
	if budget := rowBudget(terminalColumns()); budget > 0 {
		used := 1 // leading space
		if motion != "" {
			used += runewidth.StringWidth(motion) + 1
		}
		text = fitText(text, budget-used)
	}
	return formatAgentStatusMotion(text, motion)
}

func formatAgentStatusMotion(text, motion string) string {
	if motion == "" {
		return fmt.Sprintf(" \033[90m%s\033[0m", text)
	}
	return fmt.Sprintf(" %s%s\033[0m \033[90m%s\033[0m", agentConversationLiveRailStyle, motion, text)
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
