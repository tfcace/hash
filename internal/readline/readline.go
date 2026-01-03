package readline

import (
	"errors"
	"io"

	"github.com/chzyer/readline"
	"github.com/tfcace/hash/internal/history"
)

// ErrHistorySearch is returned when Ctrl+R is pressed to signal the shell
// should launch the history picker with full terminal control.
var ErrHistorySearch = errors.New("history search requested")

// Config configures the readline behavior.
type Config struct {
	Prompt              string
	Keybindings         string // "emacs", "vim", "helix"
	HistoryFile         string
	HistoryLimit        int
	Completer           *CompleterAdapter // Optional completer
	HistoryStore        *history.Store    // For Ctrl+R search
	FuncFilterInputRune func(rune) (rune, bool) // Optional: Filter input runes
}

// Readline handles command line input.
type Readline struct {
	instance     *readline.Instance
	config       Config
	historyStore *history.Store // Store reference for Ctrl+R
}

// New creates a new Readline instance.
func New(cfg Config) (*Readline, error) {
	if cfg.HistoryLimit == 0 {
		cfg.HistoryLimit = 1000
	}

	rlConfig := &readline.Config{
		Prompt:              cfg.Prompt,
		HistoryFile:         cfg.HistoryFile,
		HistoryLimit:        cfg.HistoryLimit,
		InterruptPrompt:     "^C",
		EOFPrompt:           "exit",
		FuncFilterInputRune: cfg.FuncFilterInputRune,
	}

	// Configure vim mode if requested
	if cfg.Keybindings == "vim" || cfg.Keybindings == "helix" {
		rlConfig.VimMode = true
	}

	// Configure completer if provided
	if cfg.Completer != nil {
		rlConfig.AutoComplete = cfg.Completer
	}

	instance, err := readline.NewEx(rlConfig)
	if err != nil {
		return nil, err
	}

	return &Readline{
		instance:     instance,
		config:       cfg,
		historyStore: cfg.HistoryStore,
	}, nil
}

// ReadLine reads a line of input.
func (r *Readline) ReadLine() (string, error) {
	return r.instance.Readline()
}

// SetPrompt updates the prompt.
func (r *Readline) SetPrompt(prompt string) {
	r.instance.SetPrompt(prompt)
}

// SetBuffer sets the readline buffer content.
func (r *Readline) SetBuffer(content string) {
	if r.instance != nil && r.instance.Operation != nil {
		r.instance.Operation.SetBuffer(content)
	}
}

// Clean clears readline's display state before launching another TUI.
func (r *Readline) Clean() {
	if r.instance != nil && r.instance.Operation != nil {
		r.instance.Operation.Clean()
	}
}

// Refresh restores readline's display after another TUI exits.
func (r *Readline) Refresh() {
	if r.instance != nil && r.instance.Operation != nil {
		r.instance.Operation.Refresh()
	}
}

// Close releases resources.
func (r *Readline) Close() error {
	return r.instance.Close()
}

// IsInterrupt returns true if the error is an interrupt (Ctrl+C).
func IsInterrupt(err error) bool {
	return err == readline.ErrInterrupt
}

// IsEOF returns true if the error is EOF (Ctrl+D).
func IsEOF(err error) bool {
	return err == io.EOF
}

// IsHistorySearch returns true if the error signals history search.
func IsHistorySearch(err error) bool {
	return err == ErrHistorySearch
}
