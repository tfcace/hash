package shell

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/progress"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Braille spinner frames for animation
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// ConfirmAction represents the user's response to a command suggestion.
type ConfirmAction int

const (
	ConfirmRun    ConfirmAction = iota // User pressed Enter - run the command
	ConfirmEdit                        // User pressed Tab - edit the command
	ConfirmCancel                      // User pressed Esc - cancel
)

// ConfirmationType determines which confirmation options to show.
type ConfirmationType int

const (
	ConfirmTypeCommand     ConfirmationType = iota // [Enter: run] [Tab: edit] [Esc: cancel]
	ConfirmTypeExplanation                         // [Enter: ok] [Tab: copy] [Esc: cancel]
	ConfirmTypeError                               // [Enter: retry] [Esc: cancel]
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
		return "Connecting to agent..."
	case AgentStateSending:
		return "Sending context..."
	case AgentStateThinking:
		return "Agent thinking..."
	case AgentStateReceiving:
		return "Receiving response..."
	default:
		return "Processing..."
	}
}

// ResponseUI handles displaying agent responses.
type ResponseUI struct {
	out      io.Writer
	in       *os.File
	progress *progress.OSC

	// Spinner state
	spinnerMu      sync.Mutex
	spinnerRunning bool
	spinnerStop    chan struct{}
	spinnerDone    chan struct{} // signals when spinner goroutine has exited
	spinnerText    string
}

// NewResponseUI creates a new response UI.
func NewResponseUI(out io.Writer) *ResponseUI {
	return &ResponseUI{
		out:      out,
		in:       os.Stdin,
		progress: progress.NewOSC(out),
	}
}

// ShowResponse displays an agent response.
func (u *ResponseUI) ShowResponse(resp agent.Response) {
	switch resp.Type {
	case agent.ResponseTypeCommand:
		u.showCommand(resp.Command)
	case agent.ResponseTypeExplanation:
		u.showExplanation(resp.Explanation)
	case agent.ResponseTypeError:
		u.showError(resp.Error)
	}
}

func (u *ResponseUI) showCommand(cmd string) {
	// Green arrow, command, then help text - write together to avoid split output
	output := fmt.Sprintf("\033[32m→\033[0m %s\n  \033[90m[Enter: run] [Tab: edit] [Esc: cancel]\033[0m\n", cmd)
	fmt.Fprint(u.out, output)
	// Flush if output is buffered
	switch w := u.out.(type) {
	case *os.File:
		w.Sync()
	case *bufio.Writer:
		w.Flush()
	}
}

func (u *ResponseUI) showExplanation(text string) {
	// Render markdown for explanations
	rendered := markdown.Render(text)
	fmt.Fprintln(u.out, rendered)
}

func (u *ResponseUI) showError(err string) {
	// Red text for errors
	fmt.Fprintf(u.out, "\033[31mAgent error: %s\033[0m\n", err)
}

// ShowState displays the current agent state with animated spinner.
// If a spinner is already running, it updates the text.
// If no spinner is running, it starts one.
func (u *ResponseUI) ShowState(state AgentState) {
	u.spinnerMu.Lock()
	defer u.spinnerMu.Unlock()

	text := state.String()

	if u.spinnerRunning {
		// Update the spinner text
		u.spinnerText = text
		return
	}

	// Start new spinner
	u.spinnerText = text
	u.spinnerStop = make(chan struct{})
	u.spinnerDone = make(chan struct{})
	u.spinnerRunning = true

	// Start OSC progress bar
	u.progress.Start()

	go u.runSpinner()
}

// runSpinner animates the spinner until stopped.
func (u *ResponseUI) runSpinner() {
	defer close(u.spinnerDone)

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	frame := 0
	for {
		select {
		case <-u.spinnerStop:
			return
		case <-ticker.C:
			u.spinnerMu.Lock()
			text := u.spinnerText
			u.spinnerMu.Unlock()

			char := spinnerFrames[frame%len(spinnerFrames)]
			fmt.Fprintf(u.out, "\r\033[K\033[90m%c %s\033[0m", char, text)
			frame++
		}
	}
}

// StopSpinner stops the animated spinner if running and waits for it to exit.
func (u *ResponseUI) StopSpinner() {
	u.spinnerMu.Lock()
	if !u.spinnerRunning {
		u.spinnerMu.Unlock()
		return
	}

	close(u.spinnerStop)
	done := u.spinnerDone
	u.spinnerRunning = false
	u.spinnerMu.Unlock()

	// Wait for the spinner goroutine to exit before returning
	<-done
	u.progress.Done()
}

// ShowStateWithSize displays state with context size info.
func (u *ResponseUI) ShowStateWithSize(state AgentState, sizeBytes int) {
	u.spinnerMu.Lock()
	defer u.spinnerMu.Unlock()

	sizeStr := formatSize(sizeBytes)
	text := fmt.Sprintf("%s (%s)", state.String(), sizeStr)

	if u.spinnerRunning {
		u.spinnerText = text
		return
	}

	// Start new spinner with size info
	u.spinnerText = text
	u.spinnerStop = make(chan struct{})
	u.spinnerDone = make(chan struct{})
	u.spinnerRunning = true
	u.progress.Start()

	go u.runSpinner()
}

func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
}

// ShowThinking displays a thinking indicator with optional model name.
//
// Deprecated: Use ShowState(AgentStateThinking) instead for consistent styling.
func (u *ResponseUI) ShowThinking(model string) {
	u.ShowState(AgentStateThinking)
}

// ClearThinking clears the thinking indicator.
func (u *ResponseUI) ClearThinking() {
	u.StopSpinner()
	fmt.Fprintf(u.out, "\r\033[K")
}

// StartProgress starts the OSC 9;4 progress bar without showing text.
func (u *ResponseUI) StartProgress() {
	u.progress.Start()
}

// StopProgress stops the OSC 9;4 progress bar.
func (u *ResponseUI) StopProgress() {
	u.progress.Done()
}

// WaitForConfirmation waits for user to press Enter, Tab, or Esc.
func (u *ResponseUI) WaitForConfirmation() ConfirmAction {
	fd := int(u.in.Fd())
	if !term.IsTerminal(fd) {
		return ConfirmCancel
	}

	// Enter raw mode
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return ConfirmCancel
	}
	defer term.Restore(fd, oldState)

	// Read one byte at a time
	buf := make([]byte, 3)
	for {
		n, err := u.in.Read(buf[:1])
		if err != nil || n == 0 {
			return ConfirmCancel
		}

		switch buf[0] {
		case '\r', '\n': // Enter
			return ConfirmRun
		case '\t': // Tab
			return ConfirmEdit
		case 0x1b: // Escape - might be standalone or start of sequence
			// Use channel-based timeout since SetReadDeadline doesn't work on terminals
			if u.isStandaloneEscape(buf[1:]) {
				return ConfirmCancel
			}
			// Escape sequence (arrow keys, etc.) - ignore and continue
			continue
		case 0x03: // Ctrl+C
			return ConfirmCancel
		}
		// Ignore other keys
	}
}

// ShowThinkingInline displays thinking indicator (for streaming modes).
//
// Deprecated: Use ShowState(AgentStateThinking) instead for consistent styling.
func (u *ResponseUI) ShowThinkingInline(model string) {
	u.ShowState(AgentStateThinking)
}

// ClearLine clears the current line (for replacing thinking with response).
func (u *ResponseUI) ClearLine() {
	u.StopSpinner()
	fmt.Fprintf(u.out, "\r\033[K")
}

// ClearLines moves cursor up n lines and clears each one.
// Used to remove streamed response when user cancels.
func (u *ResponseUI) ClearLines(n int) {
	for i := 0; i < n; i++ {
		fmt.Fprintf(u.out, "\033[A\033[K") // Move up, clear line
	}
}

// ShowStreamedResponse displays a streamed response with dim styling.
// The response appears tentative until confirmed.
func (u *ResponseUI) ShowStreamedResponse(text string, isCommand bool) {
	// Dim styling for tentative response
	fmt.Fprintf(u.out, "\033[90m%s\033[0m\n", text)
}

// ShowError displays an error message in the response area.
func (u *ResponseUI) ShowError(errMsg string) {
	fmt.Fprintf(u.out, "\033[31m✗ %s\033[0m\n", errMsg)
}

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
		fmt.Fprintf(u.out, "\033[90m  • Is it in your PATH? (try: which %s)\033[0m\n", command)
		fmt.Fprintf(u.out, "\033[90m  • Is it executable? (try: ls -la $(which %s))\033[0m\n", command)
	}
	fmt.Fprintln(u.out)
}

// ShowConfirmation displays the compact confirmation UI below the response.
//
// Deprecated: Use AgentOutputCoordinator.ShowHints instead for proper
// coordination with streaming output and permission prompts.
func (u *ResponseUI) ShowConfirmation(ct ConfirmationType) {
	var hint string
	switch ct {
	case ConfirmTypeCommand:
		hint = "[Enter: run] [Tab: edit] [Esc: cancel]"
	case ConfirmTypeExplanation:
		hint = "[Enter: ok] [Tab: copy] [Esc: cancel]"
	case ConfirmTypeError:
		hint = "[Enter: retry] [Esc: cancel]"
	}
	fmt.Fprintf(u.out, "  \033[90m%s\033[0m\n", hint)
}

// WaitForConfirmationByType waits for confirmation based on response type.
// Returns ConfirmAction appropriate for the confirmation type.
func (u *ResponseUI) WaitForConfirmationByType(ct ConfirmationType) ConfirmAction {
	fd := int(u.in.Fd())
	if !term.IsTerminal(fd) {
		return ConfirmCancel
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return ConfirmCancel
	}
	defer term.Restore(fd, oldState)

	buf := make([]byte, 3)
	for {
		n, err := u.in.Read(buf[:1])
		if err != nil || n == 0 {
			return ConfirmCancel
		}

		switch buf[0] {
		case '\r', '\n': // Enter
			return ConfirmRun // ConfirmRun means "primary action" (run/ok/retry)
		case '\t': // Tab
			if ct == ConfirmTypeError {
				continue // No Tab action for errors
			}
			return ConfirmEdit // ConfirmEdit means "secondary action" (edit/copy)
		case 0x1b: // Escape - might be standalone or start of sequence
			// Use channel-based timeout since SetReadDeadline doesn't work on terminals
			if u.isStandaloneEscape(buf[1:]) {
				return ConfirmCancel
			}
			// Escape sequence (arrow keys, etc.) - ignore and continue
			continue
		case 0x03: // Ctrl+C
			return ConfirmCancel
		}
	}
}

// isStandaloneEscape checks if more bytes are available within a timeout to determine if
// ESC was pressed alone or as part of an escape sequence (like arrow keys).
// Uses poll-based I/O to avoid goroutine leaks.
func (u *ResponseUI) isStandaloneEscape(buf []byte) bool {
	fd := int(u.in.Fd())

	// Use select/poll to check if more data arrives within 50ms
	var readSet unix.FdSet
	readSet.Set(fd)
	tv := unix.NsecToTimeval((50 * time.Millisecond).Nanoseconds())

	n, err := unix.Select(fd+1, &readSet, nil, nil, &tv)
	if err != nil || n == 0 {
		// Timeout or error - standalone ESC
		return true
	}

	// Data available - read it to determine if escape sequence
	if readSet.IsSet(fd) {
		nRead, err := syscall.Read(fd, buf)
		if err != nil || nRead == 0 {
			return true
		}
		return false // Got more bytes, this is an escape sequence
	}

	return true
}
