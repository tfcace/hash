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
