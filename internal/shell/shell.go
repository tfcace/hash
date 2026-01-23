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
	hashcontext "github.com/tfcace/hash/internal/context"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/readline"
	"github.com/tfcace/hash/internal/trace"
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
	editorCfg    editor.Config          // Editor configuration
	useEditor    bool                   // Use editor instead of readline
	agentHandler *AgentHandler
	responseUI   *ResponseUI
	history      *history.Store
	learning     *learning.FixStore
	clipboard    *clipboard.Buffer
	colorPalette prompt.Palette

	lastExitCode int
	lastDuration time.Duration
	lastCommand  string // Last executed command
	lastStderr   string // Stderr from last command (truncated)
	lastCwd      string // Working directory of last command

	// History navigation state for editor mode
	historyIndex     int    // -1 means current line (not in history)
	historySavedLine string // Saved current line when navigating into history

	// Context picker state
	selectedContext *hashcontext.Collection // nil = use auto-detect defaults
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
	router.SetFuzzy(cfg.Completions.Fuzzy)

	fileCompleter := completion.NewFileCompleter()
	fileCompleter.SetFuzzyMode(cfg.Completions.Fuzzy)
	router.Register(fileCompleter, completion.PriorityFilesystem)

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

	// Initialize clipboard buffer (configurable size and output limit)
	clipboardBuf := clipboard.NewBuffer(cfg.Clipboard.BufferSize)
	maxOutputSizeStr := cfg.Clipboard.MaxOutputSize
	if env := strings.TrimSpace(os.Getenv("HASH_CLIPBOARD_MAX_OUTPUT_SIZE")); env != "" {
		maxOutputSizeStr = env
	}
	if maxOutputSizeStr != "" {
		maxOutputSize, err := config.ParseSize(maxOutputSizeStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash: warning: invalid clipboard max_output_size %q: %v\n", maxOutputSizeStr, err)
		} else {
			exec.SetCaptureLimit(maxOutputSize)
			if maxOutputSize < 0 {
				clipboardBuf.SetMaxOutputSize(-1)
			} else {
				maxInt := int(^uint(0) >> 1)
				if maxOutputSize > int64(maxInt) {
					clipboardBuf.SetMaxOutputSize(maxInt)
				} else {
					clipboardBuf.SetMaxOutputSize(int(maxOutputSize))
				}
			}
		}
	}

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

	// Re-extract color palette now that PATH is set up (starship may now be findable)
	s.refreshColorPalette()

	s.updatePrompt()
	trace.ShellHigh("prompt_start", map[string]any{
		"mode": "editor",
	})

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

		trace.ShellDetailed("input_ready", map[string]any{
			"line":   line,
			"error":  errStr(err),
			"editor": s.useEditor,
		})

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

		// Handle !! shortcut for quick issue submission
		if line == "!!" {
			if s.lastExitCode == 0 {
				fmt.Print("Last command succeeded. Open issue anyway? [y/N] ")
				var response string
				fmt.Scanln(&response)
				if strings.ToLower(response) != "y" {
					s.updatePrompt()
					continue
				}
			}
			s.builtinIssue([]string{"--last"})
			s.updatePrompt()
			continue
		}

		if line == "" {
			s.updatePrompt()
			continue
		}

		// Parse the line
		parsed := parser.Parse(line)

		trace.ShellHigh("dispatch", map[string]any{
			"type":    parsed.Type.String(),
			"command": parsed.Command,
			"prompt":  parsed.AgentPrompt,
		})

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

			// Capture stderr for issue reporting
			stderrCap := newStderrCapture(os.Stderr)

			// Execute external command
			result, err := s.executor.Execute(ctx, line, os.Stdout, stderrCap)
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

			// Store for issue reporting
			s.lastCommand = line
			s.lastStderr = stderrCap.String()
			s.lastCwd, _ = os.Getwd()

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
		if result.ContextPicker {
			// Launch context picker
			s.runContextPicker()
			// Re-run editor with previous text
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

// runContextPicker launches the context picker TUI and stores selections.
func (s *Shell) runContextPicker() {
	// Build collection with available context
	builder := hashcontext.NewBuilder().AutoDetect()

	// Add recent history if available
	if s.history != nil {
		entries, _ := s.history.Search(history.SearchOptions{Limit: 10})
		commands := make([]string, len(entries))
		for i, e := range entries {
			commands[i] = e.Command
		}
		builder.WithHistory(commands)
	}

	// Add last output/error from clipboard buffer
	if s.clipboard != nil {
		if output := s.clipboard.LastOutput(); output != "" {
			builder.WithLastOutput(output)
		}
	}

	// Add common env vars as options
	builder.WithEnvVars([]string{"GOPATH", "HOME", "PATH", "EDITOR"})

	collection := builder.Build()

	// Run the picker UI
	picker := hashcontext.NewPickerUI(collection)
	_, err := picker.Run()
	if err != nil {
		// Picker cancelled or errored, keep existing selection
		return
	}

	// Store the collection (with user's selections)
	s.selectedContext = collection
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
	timeout := 30 * time.Second
	if s.config.Agent.Timeout != "" {
		if parsedDur, err := time.ParseDuration(s.config.Agent.Timeout); err == nil {
			timeout = parsedDur
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Pass selected context to agent handler
	s.agentHandler.SetSelectedContext(s.selectedContext)

	// Use unified streaming handler for all modes
	s.handleAgentRequestUnified(ctx, parsed)
}

// handleAgentInlineStreaming uses ghost text for inline completions.
func (s *Shell) handleAgentInlineStreaming(ctx context.Context, parsed parser.ParseResult, modelName string) {
	// Start streaming request
	textCh, errCh := s.agentHandler.StreamRequest(ctx, parsed)

	// Build initial text for editor
	initialText := parsed.Command

	// Reset history navigation
	s.historyIndex = -1
	s.historySavedLine = ""

	// Configure editor with ghost text streaming
	cfg := s.editorCfg
	cfg.Prompt = s.currentPromptLine()

	ed := editor.New(cfg, os.Stdin, os.Stdout)
	if initialText != "" {
		ed.SetInitialText(initialText)
	}

	// Set up streaming ghost text with model name
	ed.SetGhostTextStreaming(textCh, errCh)
	ed.SetStreamingModel(modelName)

	// Run editor
	result, err := ed.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: editor error: %v\n", err)
		s.lastExitCode = 1
		s.updatePrompt()
		return
	}

	if result.Cancelled {
		s.updatePrompt()
		return
	}

	if result.EOF {
		fmt.Println("exit")
		os.Exit(0)
	}

	// User accepted the command
	command := strings.TrimSpace(result.Text)
	if command == "" {
		s.updatePrompt()
		return
	}

	// Stop progress bar before executing (defer in caller will be a no-op)
	s.responseUI.StopProgress()

	// Execute the command
	execResult, err := s.executor.Execute(ctx, command, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		s.lastExitCode = 1
	} else {
		s.lastExitCode = execResult.ExitCode
		s.lastDuration = execResult.Duration
	}
	s.recordCommand(command, s.lastExitCode, s.lastDuration)
	s.updatePrompt()
}

// executePipeCommand runs the pipe command and captures its output.
// Returns the captured output string, or error if execution failed.
func (s *Shell) executePipeCommand(ctx context.Context, command string) (string, error) {
	var outputBuf strings.Builder

	// Execute with output captured but not displayed
	result, err := s.executor.Execute(ctx, command, &outputBuf, os.Stderr)
	if err != nil {
		return "", err
	}

	if result.ExitCode != 0 {
		return outputBuf.String(), fmt.Errorf("command exited with code %d", result.ExitCode)
	}

	return outputBuf.String(), nil
}

// handleAgentRequestUnified handles all ?? modes with streaming UX.
func (s *Shell) handleAgentRequestUnified(ctx context.Context, parsed parser.ParseResult) {
	modelName := s.config.Agent.Model
	if modelName == "" {
		modelName = s.config.Agent.Command
	}

	// For pipe mode, capture command output first
	var pipeOutput string
	if parsed.Type == parser.CommandTypeAgentPipe {
		var err error
		pipeOutput, err = s.executePipeCommand(ctx, parsed.Command)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash: pipe command failed: %v\n", err)
			s.lastExitCode = 1
			s.updatePrompt()
			return
		}
		// Update context with pipe output
		if s.clipboard != nil {
			s.clipboard.SetOutput(pipeOutput)
		}
	}

	// Start progress bar
	s.responseUI.StartProgress()
	defer s.responseUI.StopProgress()

	// For full ?? and pipe modes, show thinking on new line
	// For inline mode, use editor with ghost text
	if s.useEditor && parsed.Type == parser.CommandTypeAgentInline {
		s.handleAgentInlineStreaming(ctx, parsed, modelName)
		return
	}

	// Full ?? and pipe modes: streaming with confirmation UI
	s.handleAgentFullStreaming(ctx, parsed, modelName)
}

// handleAgentFullStreaming handles full ?? and pipe modes with streaming.
func (s *Shell) handleAgentFullStreaming(ctx context.Context, parsed parser.ParseResult, modelName string) {
	// Show thinking indicator
	s.responseUI.ShowThinkingInline(modelName)

	// Start streaming request
	textCh, errCh := s.agentHandler.StreamRequest(ctx, parsed)

	// Collect streamed response
	var response strings.Builder
	var streamErr error
	lineCount := 0 // Track lines for clearing on cancel

collectLoop:
	for {
		select {
		case <-ctx.Done():
			s.responseUI.ClearLine()
			fmt.Fprintln(os.Stderr, "hash: request cancelled")
			s.lastExitCode = 1
			s.updatePrompt()
			return
		case err, ok := <-errCh:
			if !ok {
				errCh = nil // Stop selecting on closed channel
				continue
			}
			if err != nil {
				streamErr = err
			}
		case text, ok := <-textCh:
			if !ok {
				break collectLoop
			}
			if response.Len() == 0 {
				// First chunk - clear thinking indicator
				s.responseUI.ClearLine()
			}
			response.WriteString(text)
			// Count newlines for clearing on cancel
			lineCount += strings.Count(text, "\n")
			// Stream output character by character (dim)
			fmt.Fprintf(os.Stdout, "\033[90m%s\033[0m", text)
		}
	}

	fmt.Println() // New line after response
	lineCount++   // Count the final newline

	if streamErr != nil {
		s.responseUI.ShowError(streamErr.Error())
		s.responseUI.ShowConfirmation(ConfirmTypeError)
		action := s.responseUI.WaitForConfirmationByType(ConfirmTypeError)
		fmt.Println()
		if action == ConfirmRun { // Retry
			s.handleAgentFullStreaming(ctx, parsed, modelName)
			return
		}
		// Cancel: clear error + any partial response
		// lineCount + error line + confirmation line + blank line
		s.responseUI.ClearLines(lineCount + 3)
		s.updatePrompt()
		return
	}

	// Determine response type
	responseText := strings.TrimSpace(response.String())
	collector := agent.NewStreamCollector()
	collector.Append(responseText)
	resp := collector.Response()

	// Show confirmation based on response type
	var confirmType ConfirmationType
	if resp.Type == agent.ResponseTypeCommand {
		confirmType = ConfirmTypeCommand
	} else {
		confirmType = ConfirmTypeExplanation
	}

	s.responseUI.ShowConfirmation(confirmType)
	action := s.responseUI.WaitForConfirmationByType(confirmType)
	fmt.Println()

	// Stop progress bar before any action
	s.responseUI.StopProgress()

	switch action {
	case ConfirmRun:
		if confirmType == ConfirmTypeCommand {
			// Execute the command
			result, err := s.executor.Execute(ctx, resp.Command, os.Stdout, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: %v\n", err)
				s.lastExitCode = 1
			} else {
				s.lastExitCode = result.ExitCode
				s.lastDuration = result.Duration
			}
			s.recordCommand(resp.Command, s.lastExitCode, s.lastDuration)
		}
		// For explanations, ConfirmRun just dismisses
	case ConfirmEdit:
		if confirmType == ConfirmTypeCommand {
			// Put command in readline buffer for editing
			if s.useEditor {
				// For editor mode, we need to re-enter with the command
				s.handleEditCommand(ctx, resp.Command)
				return
			} else {
				s.readline.SetBuffer(resp.Command)
			}
		} else {
			// Copy explanation to clipboard
			if s.clipboard != nil {
				s.clipboard.AddCommand(responseText) // Reuse for now
			}
		}
	case ConfirmCancel:
		// Clear the streamed response from screen
		// +1 for confirmation hint line, +1 for the blank line after fmt.Println()
		s.responseUI.ClearLines(lineCount + 2)
	}

	s.updatePrompt()
}

// handleEditCommand opens editor with command for editing.
func (s *Shell) handleEditCommand(ctx context.Context, command string) {
	s.historyIndex = -1
	s.historySavedLine = ""

	cfg := s.editorCfg
	cfg.Prompt = s.currentPromptLine()

	ed := editor.New(cfg, os.Stdin, os.Stdout)
	ed.SetInitialText(command)

	result, err := ed.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: editor error: %v\n", err)
		s.lastExitCode = 1
		s.updatePrompt()
		return
	}

	if result.Cancelled || result.EOF {
		s.updatePrompt()
		return
	}

	// Execute edited command
	editedCmd := strings.TrimSpace(result.Text)
	if editedCmd != "" {
		execResult, err := s.executor.Execute(ctx, editedCmd, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash: %v\n", err)
			s.lastExitCode = 1
		} else {
			s.lastExitCode = execResult.ExitCode
			s.lastDuration = execResult.Duration
		}
		s.recordCommand(editedCmd, s.lastExitCode, s.lastDuration)
	}
	s.updatePrompt()
}

func (s *Shell) updatePrompt() {
	s.printPromptPrefix()
}

// printPromptPrefix prints just the Starship prefix (info bar) and updates the
// readline prompt. This is called when returning from the Ctrl+R history picker
// or when updating the prompt after command execution.
func (s *Shell) printPromptPrefix() {
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

// refreshColorPalette re-extracts colors from starship after PATH is set up.
// This is called after startup files are sourced, when starship may now be findable.
func (s *Shell) refreshColorPalette() {
	// Trigger lazy starship lookup by generating a prompt
	// This sets p.starshipPath if starship is now in PATH
	cwd, _ := os.Getwd()
	s.prompt.Generate(prompt.PromptContext{Cwd: cwd})

	// Now try to extract palette with the (hopefully found) starship path
	newPalette := prompt.ExtractPalette(s.prompt.StarshipPath())

	// Only update if we got actual starship colors (not defaults)
	// Check if Primary color differs from default - indicates successful extraction
	if newPalette.Primary != prompt.DefaultPalette().Primary {
		s.colorPalette = newPalette
		// Update editor config with new colors
		s.editorCfg.InputBgColor = newPalette.InputBg
		s.editorCfg.ScrollbarColor = newPalette.Primary
	}
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
				Text:        result.Prefix + item.Value,
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

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
