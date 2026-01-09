package shell

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/progress"
	"golang.org/x/term"
)

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
	ConfirmTypeCommand     ConfirmationType = iota // ↵ run · ⇥ edit · esc
	ConfirmTypeExplanation                         // ↵ ok · ⇥ copy · esc
	ConfirmTypeError                               // ↵ retry · esc
)

// ResponseUI handles displaying agent responses.
type ResponseUI struct {
	out      io.Writer
	in       *os.File
	progress *progress.OSC
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
	if f, ok := u.out.(*os.File); ok {
		f.Sync()
	} else if bw, ok := u.out.(*bufio.Writer); ok {
		bw.Flush()
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

// ShowThinking displays a thinking indicator with optional model name.
func (u *ResponseUI) ShowThinking(model string) {
	if model != "" {
		fmt.Fprintf(u.out, "\033[90m⟳ Thinking (%s)...\033[0m", model)
	} else {
		fmt.Fprintf(u.out, "\033[90m⟳ Thinking...\033[0m")
	}
	// Show OSC 9;4 progress bar in terminal tab/title bar
	u.progress.Start()
}

// ClearThinking clears the thinking indicator.
func (u *ResponseUI) ClearThinking() {
	fmt.Fprintf(u.out, "\r\033[K")
	// Clear OSC 9;4 progress bar
	u.progress.Done()
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
			// Try to read more to see if it's an escape sequence
			u.in.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, _ = u.in.Read(buf[1:])
			u.in.SetReadDeadline(time.Time{})
			if n == 0 {
				// Standalone Esc
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
// Unlike ShowThinking, this doesn't start progress bar (caller manages that).
func (u *ResponseUI) ShowThinkingInline(model string) {
	if model != "" {
		fmt.Fprintf(u.out, "\033[90m⟳ thinking (%s)...\033[0m", model)
	} else {
		fmt.Fprintf(u.out, "\033[90m⟳ thinking...\033[0m")
	}
}

// ClearLine clears the current line (for replacing thinking with response).
func (u *ResponseUI) ClearLine() {
	fmt.Fprintf(u.out, "\r\033[K")
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

// ShowConfirmation displays the compact confirmation UI below the response.
func (u *ResponseUI) ShowConfirmation(ct ConfirmationType) {
	var hint string
	switch ct {
	case ConfirmTypeCommand:
		hint = "↵ run · ⇥ edit · esc"
	case ConfirmTypeExplanation:
		hint = "↵ ok · ⇥ copy · esc"
	case ConfirmTypeError:
		hint = "↵ retry · esc"
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
		case 0x1b: // Escape
			u.in.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, _ = u.in.Read(buf[1:])
			u.in.SetReadDeadline(time.Time{})
			if n == 0 {
				return ConfirmCancel
			}
			continue
		case 0x03: // Ctrl+C
			return ConfirmCancel
		}
	}
}
