package readline

import (
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/history"
)

// PickerFunc is a function that launches the history picker and returns the selected command.
type PickerFunc func() string

// PromptRefreshFunc is called to refresh the full prompt (including prefix/info bar).
type PromptRefreshFunc func()

// InputHandler wraps readline to intercept special keybindings.
type InputHandler struct {
	readline          *Readline
	history           *history.Store
	clipboardBuf      *clipboard.Buffer
	pickerFunc        PickerFunc
	promptRefreshFunc PromptRefreshFunc
}

// NewInputHandler creates a new input handler.
func NewInputHandler(rl *Readline, histStore *history.Store) *InputHandler {
	return &InputHandler{
		readline: rl,
		history:  histStore,
	}
}

// SetClipboard sets the clipboard buffer for output cross-referencing.
func (ih *InputHandler) SetClipboard(buf *clipboard.Buffer) {
	ih.clipboardBuf = buf
}

// SetReadline sets the readline instance (used after creation).
func (ih *InputHandler) SetReadline(rl *Readline) {
	ih.readline = rl
}

// SetPickerFunc sets the function that launches the history picker.
func (ih *InputHandler) SetPickerFunc(fn PickerFunc) {
	ih.pickerFunc = fn
}

// SetPromptRefreshFunc sets the function that refreshes the full prompt.
func (ih *InputHandler) SetPromptRefreshFunc(fn PromptRefreshFunc) {
	ih.promptRefreshFunc = fn
}

// SetReadlineBuffer sets the content in readline's buffer.
func (ih *InputHandler) SetReadlineBuffer(content string) {
	if ih.readline != nil {
		ih.readline.SetBuffer(content)
	}
}

// HandleCtrlR launches the history picker and returns the selected command.
// This is called from the FuncFilterInputRune callback.
func (ih *InputHandler) HandleCtrlR() {
	if ih.pickerFunc == nil {
		return
	}

	// Clean readline's display before launching the picker
	if ih.readline != nil {
		ih.readline.Clean()
	}

	// Launch the picker
	selected := ih.pickerFunc()

	// Set the selected command in the buffer
	if selected != "" {
		ih.SetReadlineBuffer(selected)
	}

	// Refresh the full prompt (including prefix/info bar) after the picker exits
	if ih.promptRefreshFunc != nil {
		ih.promptRefreshFunc()
	}

	// Refresh readline's display after the picker exits
	if ih.readline != nil {
		ih.readline.Refresh()
	}
}

// ReadLine reads a line from the user.
func (ih *InputHandler) ReadLine() (string, error) {
	return ih.readline.ReadLine()
}

// HistoryStore returns the history store for launching the picker.
func (ih *InputHandler) HistoryStore() *history.Store {
	return ih.history
}

// ClipboardBuffer returns the clipboard buffer for the picker.
func (ih *InputHandler) ClipboardBuffer() *clipboard.Buffer {
	return ih.clipboardBuf
}
