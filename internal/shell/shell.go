package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/readline"
)

// Mode represents the shell's startup mode.
type Mode struct {
	Login       bool // Login shell (sources profile files)
	Interactive bool // Interactive shell (has TTY)
}

// Shell is the main Hash shell instance.
type Shell struct {
	mode         Mode // Startup mode
	config       *config.Config
	executor     *executor.Executor
	prompt       *prompt.Prompt
	readline     *readline.Readline
	inputHandler *readline.InputHandler // For Ctrl+R search
	editorCfg    editor.Config           // Editor configuration
	useEditor    bool                    // Use editor instead of readline
	agentHandler *AgentHandler
	responseUI   *ResponseUI
	history      *history.Store
	learning     *learning.FixStore
	clipboard    *clipboard.Buffer
	colorPalette prompt.Palette

	lastExitCode int
	lastDuration time.Duration

	// History navigation state for editor mode
	historyIndex   int    // -1 means current line (not in history)
	historySavedLine string // Saved current line when navigating into history
}

// New creates a new Shell instance.
func New(cfg *config.Config) (*Shell, error) {
	exec := executor.New()

	promptCfg := prompt.Config{
		Mode:         cfg.Prompt.Mode,
		StarshipPath: cfg.Prompt.StarshipPath,
		Alignment:    cfg.Prompt.Alignment,
		DevMode:      cfg.Prompt.DevMode,
		DevModeLabel: cfg.Prompt.DevModeLabel,
	}
	p := prompt.New(promptCfg)

	// Extract color palette from starship for consistent UI theming
	colorPalette := prompt.ExtractPalette(p.StarshipPath())

	cwd, _ := os.Getwd()
	_, initialPrompt := p.GenerateMultiLine(prompt.PromptContext{
		Cwd:      cwd,
		ExitCode: 0,
	})

	// Set up completion router
	router := completion.NewRouter()
	router.Register(completion.NewFileCompleter(), completion.PriorityFilesystem)

	if cfg.Completions.CobraEnabled {
		router.Register(completion.NewCobraCompleter(), completion.PriorityToolNative)
	}

	// Set up agent (for both ?? commands and completions)
	var agentHandler *AgentHandler
	var agentClient *agent.Client

	// Select transport based on config
	var transport agent.Transport
	switch cfg.Agent.Transport {
	case "http":
		if cfg.Agent.URL != "" {
			transport = agent.NewHTTPTransport(agent.HTTPConfig{
				URL:   cfg.Agent.URL,
				Model: cfg.Agent.Model,
			})
		}
	default: // "stdio" or "acp" or unset - use ACP protocol
		if cfg.Agent.Command != "" {
			transport = agent.NewACPTransport(agent.ACPConfig{
				Command: cfg.Agent.Command,
				Args:    cfg.Agent.Args,
			})
		}
	}

	if transport != nil {
		agentClient = agent.NewClient(transport)
		agentHandler = NewAgentHandler(agentClient)

		// Add agent completer for ?? inline
		router.Register(completion.NewAgentCompleter(agentClient), completion.PriorityAgent)
	}

	completerAdapter := readline.NewCompleterAdapter(router)

	// Initialize history store (must happen before readline creation)
	var historyStore *history.Store
	historyPath := filepath.Join(getDataDir(), "history.db")
	var err error
	historyStore, err = history.NewStore(historyPath)
	if err != nil {
		// Log warning but don't fail - history is optional
		fmt.Fprintf(os.Stderr, "hash: warning: history unavailable: %v\n", err)
	}

	// Create input handler and Ctrl+R filter
	inputHandler := readline.NewInputHandler(nil, historyStore) // will be set after readline creation

	rlCfg := readline.Config{
		Prompt:       initialPrompt,
		Keybindings:  cfg.Shell.Keybindings,
		Completer:    completerAdapter,
		HistoryStore: historyStore,
		FuncFilterInputRune: func(r rune) (rune, bool) {
			// Ctrl+R is ASCII 18
			if r == 18 {
				// Launch history picker directly from callback
				// HandleCtrlR manages terminal cleanup and refresh
				inputHandler.HandleCtrlR()
				return 0, false
			}
			// All other runes pass through normally
			return r, true
		},
	}
	rl, err := readline.New(rlCfg)
	if err != nil {
		return nil, fmt.Errorf("readline: %w", err)
	}

	// Now set the readline instance in the input handler
	inputHandler.SetReadline(rl)

	// Initialize learning store
	var learningStore *learning.FixStore
	learningPath := filepath.Join(getDataDir(), "learning.db")
	learningStore, err = learning.NewFixStore(learningPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: warning: learning unavailable: %v\n", err)
	}

	// Initialize clipboard buffer (100 entries, 1MB max per output)
	clipboardBuf := clipboard.NewBuffer(100)
	clipboardBuf.SetMaxOutputSize(1024 * 1024) // 1MB

	// Set clipboard buffer on input handler for Ctrl+R output cross-referencing
	inputHandler.SetClipboard(clipboardBuf)

	// Set up the history picker function
	inputHandler.SetPickerFunc(func() string {
		if historyStore == nil {
			return ""
		}
		picker := history.NewSearchUI(historyStore, colorPalette)
		picker.SetClipboard(clipboardBuf)
		selected, err := picker.Run()
		if err != nil {
			return ""
		}
		return selected
	})

	// Set clipboard buffer on agent handler for context
	if agentHandler != nil {
		agentHandler.SetClipboard(clipboardBuf)
	}

	// Configure editor mode based on config
	useEditor := cfg.Input.Mode == "editor"
	editorCfg := editor.Config{
		Keybindings:    cfg.Input.Keybindings,
		Gutter:         cfg.Input.Gutter,
		InputBgColor:   colorPalette.InputBg,
		ScrollbarColor: colorPalette.Primary,
		CompleteFunc:   makeEditorCompleteFunc(router),
	}

	shell := &Shell{
		config:       cfg,
		executor:     exec,
		prompt:       p,
		readline:     rl,
		inputHandler: inputHandler,
		editorCfg:    editorCfg,
		useEditor:    useEditor,
		agentHandler: agentHandler,
		responseUI:   NewResponseUI(os.Stdout),
		history:      historyStore,
		learning:     learningStore,
		clipboard:    clipboardBuf,
		colorPalette: colorPalette,
		historyIndex: -1, // Start before history (current line)
	}

	// Set up history function for editor mode
	// This closure captures shell for proper state management
	if useEditor && historyStore != nil {
		shell.editorCfg.HistoryFunc = shell.navigateHistory
	}

	// Set prompt refresh callback for Ctrl+R history picker
	// This prints the Starship prefix (info bar) when returning from the picker
	inputHandler.SetPromptRefreshFunc(shell.printPromptPrefix)

	return shell, nil
}

// NewWithMode creates a new Shell instance with explicit mode.
func NewWithMode(cfg *config.Config, mode Mode) (*Shell, error) {
	sh, err := New(cfg)
	if err != nil {
		return nil, err
	}
	sh.mode = mode
	return sh, nil
}

// Mode returns the shell's current mode.
func (s *Shell) Mode() Mode {
	return s.mode
}

// Run starts the shell REPL.
func (s *Shell) Run(ctx context.Context) error {
	defer s.readline.Close()

	// Run startup files and commands based on mode
	if err := s.runStartup(ctx); err != nil {
		if err == errExit {
			return nil
		}
		return err
	}
	s.updatePrompt()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var line string
		var err error

		if s.useEditor {
			line, err = s.readLineWithEditor(ctx)
		} else {
			line, err = s.inputHandler.ReadLine()
		}

		if err != nil {
			if readline.IsInterrupt(err) || errors.Is(err, ErrEditorCancelled) {
				fmt.Println("^C")
				continue
			}
			if readline.IsEOF(err) || errors.Is(err, ErrEditorEOF) {
				fmt.Println("exit")
				return nil
			}
			return err
		}

		line = trimSpace(line)
		if line == "" {
			s.updatePrompt()
			continue
		}

		// Parse the line
		parsed := parser.Parse(line)

		switch parsed.Type {
		case parser.CommandTypeEmpty:
			s.updatePrompt()
			continue

		case parser.CommandTypeAgent, parser.CommandTypeAgentPipe, parser.CommandTypeAgentInline:
			s.handleAgentRequest(ctx, parsed)
			continue

		case parser.CommandTypeRegular:
			// Check for builtins first
			handled, err := s.executeBuiltin(line)
			if err == errExit {
				return nil
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: %v\n", err)
				s.lastExitCode = 1
				s.updatePrompt()
				continue
			}
			if handled {
				s.lastExitCode = 0
				s.updatePrompt()
				continue
			}

			// Record command to clipboard buffer before execution
			if s.clipboard != nil {
				s.clipboard.AddCommand(line)
			}

			// Execute external command
			result, err := s.executor.Execute(ctx, line, os.Stdout, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: %v\n", err)
				s.lastExitCode = 1
			} else {
				s.lastExitCode = result.ExitCode
				s.lastDuration = result.Duration
				// Record captured output to clipboard buffer
				if s.clipboard != nil && result.CapturedOutput != "" {
					s.clipboard.SetOutput(result.CapturedOutput)
				}
			}

			// Record command in history
			s.recordCommand(line, s.lastExitCode, s.lastDuration)
		}

		s.updatePrompt()
	}
}

func (s *Shell) runInitCommands(ctx context.Context) error {
	if s.config == nil {
		return nil
	}

	for _, raw := range s.config.Shell.InitCommands {
		line := trimSpace(raw)
		if line == "" {
			continue
		}

		parsed := parser.Parse(line)
		switch parsed.Type {
		case parser.CommandTypeEmpty:
			continue

		case parser.CommandTypeAgent, parser.CommandTypeAgentPipe, parser.CommandTypeAgentInline:
			fmt.Fprintf(os.Stderr, "hash: init_commands does not support agent syntax: %s\n", line)
			continue

		case parser.CommandTypeRegular:
			handled, err := s.executeBuiltin(line)
			if err == errExit {
				return errExit
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: init command failed: %s: %v\n", line, err)
				continue
			}
			if handled {
				continue
			}

			_, err = s.executor.Execute(ctx, line, os.Stdout, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: init command failed: %s: %v\n", line, err)
			}
		}
	}

	return nil
}

// ErrEditorCancelled is returned when the editor is cancelled (Ctrl+C).
var ErrEditorCancelled = errors.New("editor cancelled")

// ErrEditorEOF is returned when the editor receives EOF (Ctrl+D).
var ErrEditorEOF = errors.New("editor EOF")

// readLineWithEditor uses the inline editor for input.
func (s *Shell) readLineWithEditor(ctx context.Context) (string, error) {
	initialText := ""

	// Reset history navigation state for new input session
	s.historyIndex = -1
	s.historySavedLine = ""

	for {
		// Get current prompt for editor
		cfg := s.editorCfg
		cfg.Prompt = s.currentPromptLine()

		ed := editor.New(cfg, os.Stdin, os.Stdout)
		if initialText != "" {
			ed.SetInitialText(initialText)
			initialText = ""
		}
		result, err := ed.Run(ctx)
		if err != nil {
			return "", err
		}
		if result.EOF {
			return "", ErrEditorEOF
		}
		if result.Cancelled {
			return "", ErrEditorCancelled
		}
		if result.HistorySearch {
			// Launch history picker
			selected := s.runHistoryPicker()
			if selected != "" {
				// Re-run editor with selected command
				initialText = selected
				s.printPromptPrefix()
				continue
			}
			// No selection, re-run editor with previous text
			initialText = result.Text
			s.printPromptPrefix()
			continue
		}
		return result.Text, nil
	}
}

// currentPromptLine returns the current prompt line (without prefix).
func (s *Shell) currentPromptLine() string {
	cwd, _ := os.Getwd()
	ctx := prompt.PromptContext{
		Cwd:        cwd,
		ExitCode:   s.lastExitCode,
		DurationMs: s.lastDuration.Milliseconds(),
	}
	_, promptLine := s.prompt.GenerateMultiLine(ctx)
	return promptLine
}

// runHistoryPicker launches the Ctrl+R history picker and returns the selected command.
func (s *Shell) runHistoryPicker() string {
	if s.history == nil {
		return ""
	}
	picker := history.NewSearchUI(s.history, s.colorPalette)
	picker.SetClipboard(s.clipboard)
	selected, err := picker.Run()
	if err != nil {
		return ""
	}
	return selected
}

// navigateHistory handles Up/Down arrow history navigation.
// dir=-1 for Up (older history), dir=+1 for Down (newer history).
// currentLine is the current buffer content, used to save when entering history.
// Returns the command to display, or "" if at boundary.
func (s *Shell) navigateHistory(dir int, currentLine string) string {
	entries, _ := s.history.Search(history.SearchOptions{Limit: 1000})
	if len(entries) == 0 {
		return ""
	}

	// Calculate new index
	// dir=-1 (Up) means go to older history = increase index
	// dir=+1 (Down) means go to newer history = decrease index
	newIndex := s.historyIndex - dir

	// Boundary check: can't go before current line
	if newIndex < -1 {
		return ""
	}

	// Boundary check: can't go past oldest entry
	if newIndex >= len(entries) {
		return ""
	}

	// Save current line when first entering history
	if s.historyIndex == -1 && newIndex >= 0 {
		s.historySavedLine = currentLine
	}

	s.historyIndex = newIndex

	// Return to current line (what user was typing)
	if s.historyIndex == -1 {
		return s.historySavedLine
	}

	return entries[s.historyIndex].Command
}

func (s *Shell) handleAgentRequest(ctx context.Context, parsed parser.ParseResult) {
	if s.agentHandler == nil {
		fmt.Fprintf(os.Stderr, "hash: no agent configured\n")
		fmt.Fprintf(os.Stderr, "  Configure an agent in ~/.config/hash/config.toml:\n")
		fmt.Fprintf(os.Stderr, "  [agent]\n")
		fmt.Fprintf(os.Stderr, "  command = \"claude\"\n")
		s.lastExitCode = 1
		s.updatePrompt()
		return
	}

	// Apply agent timeout
	timeout := 30 * time.Second // default
	if s.config.Agent.Timeout != "" {
		if parsed, err := time.ParseDuration(s.config.Agent.Timeout); err == nil {
			timeout = parsed
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Get model name for display
	modelName := s.config.Agent.Model
	if modelName == "" {
		modelName = s.config.Agent.Command
	}
	s.responseUI.ShowThinking(modelName)

	resp, err := s.agentHandler.HandleRequest(ctx, parsed)
	s.responseUI.ClearThinking()

	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: agent error: %v\n", err)
		s.lastExitCode = 1
		s.updatePrompt()
		return
	}

	// For inline completion, reconstruct the full command
	if parsed.Type == parser.CommandTypeAgentInline && resp.Type == agent.ResponseTypeCommand {
		resp.Command = parsed.Command + resp.Command
	}

	s.responseUI.ShowResponse(resp)

	// If it's a command suggestion, wait for confirmation
	if resp.Type == agent.ResponseTypeCommand {
		action := s.responseUI.WaitForConfirmation()
		fmt.Println() // Move to next line after key press

		switch action {
		case ConfirmRun:
			// Execute the suggested command
			result, err := s.executor.Execute(ctx, resp.Command, os.Stdout, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: %v\n", err)
				s.lastExitCode = 1
			} else {
				s.lastExitCode = result.ExitCode
				s.lastDuration = result.Duration
			}
			s.recordCommand(resp.Command, s.lastExitCode, s.lastDuration)
			s.updatePrompt()
			return

		case ConfirmEdit:
			// Put the command in the input line for editing
			if s.useEditor {
				// For editor mode, we'll need to pass the text back
				// For now, just print it so user can copy
				fmt.Printf("Edit: %s\n", resp.Command)
			} else {
				// For readline mode, set the line buffer
				s.readline.SetBuffer(resp.Command)
			}
			s.updatePrompt()
			return

		case ConfirmCancel:
			// User cancelled - do nothing
			s.updatePrompt()
			return
		}
	}

	s.lastExitCode = 0
	s.updatePrompt()
}

func (s *Shell) updatePrompt() {
	cwd, _ := os.Getwd()
	ctx := prompt.PromptContext{
		Cwd:        cwd,
		ExitCode:   s.lastExitCode,
		DurationMs: s.lastDuration.Milliseconds(),
	}

	// Use GenerateMultiLine to properly handle Starship's multi-line prompts.
	// chzyer/readline doesn't support multi-line prompts, so we print the
	// prefix (info bar) ourselves and only give readline the prompt character.
	prefix, promptLine := s.prompt.GenerateMultiLine(ctx)
	if prefix != "" {
		fmt.Print(prefix)
	}
	s.readline.SetPrompt(promptLine)
	// Note: For editor mode, the editor renders the prompt itself
}

// printPromptPrefix prints just the Starship prefix (info bar) and updates the
// readline prompt. This is called when returning from the Ctrl+R history picker.
func (s *Shell) printPromptPrefix() {
	cwd, _ := os.Getwd()
	ctx := prompt.PromptContext{
		Cwd:        cwd,
		ExitCode:   s.lastExitCode,
		DurationMs: s.lastDuration.Milliseconds(),
	}

	prefix, promptLine := s.prompt.GenerateMultiLine(ctx)
	if prefix != "" {
		fmt.Print(prefix)
	}
	s.readline.SetPrompt(promptLine)
	// Note: For editor mode, the editor renders the prompt itself
}

// stripAnsi removes ANSI escape sequences for length calculation.
func stripAnsi(s string) string {
	var result []byte
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}
		result = append(result, s[i])
	}
	return string(result)
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// makeEditorCompleteFunc adapts the completion router to editor's CompleteFunc.
func makeEditorCompleteFunc(router *completion.Router) func(string, int) []editor.Completion {
	return func(line string, pos int) []editor.Completion {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		result, err := router.Complete(ctx, line, pos)
		if err != nil || len(result.Items) == 0 {
			return nil
		}

		items := make([]editor.Completion, len(result.Items))
		for i, item := range result.Items {
			items[i] = editor.Completion{
				Text:        item.Value,
				Description: item.Description,
			}
		}
		return items
	}
}

// Close releases shell resources.
func (s *Shell) Close() error {
	if s.history != nil {
		s.history.Close()
	}
	if s.learning != nil {
		s.learning.Close()
	}
	return s.readline.Close()
}

// recordCommand saves a command to history.
func (s *Shell) recordCommand(line string, exitCode int, duration time.Duration) {
	if s.history == nil {
		return
	}

	// Parse for sudo
	sudoResult := history.ParseSudoCommand(line)

	cwd, _ := os.Getwd()
	gitBranch := detectGitBranch()

	cmd := history.Command{
		Command:    sudoResult.Command,
		Cwd:        cwd,
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		Timestamp:  time.Now(),
		GitBranch:  gitBranch,
		IsSudo:     sudoResult.IsSudo,
		SudoUser:   sudoResult.SudoUser,
		RawCommand: sudoResult.RawCommand,
	}

	s.history.Add(cmd)
}

// detectGitBranch returns the current git branch, or empty string if not in a git repo.
func detectGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getDataDir returns the data directory for hash.
func getDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "hash")
}

// History returns the history store for use by builtins.
func (s *Shell) History() *history.Store {
	return s.history
}

// Learning returns the learning store for use by builtins.
func (s *Shell) Learning() *learning.FixStore {
	return s.learning
}
