package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/allowlist"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/config"
	hashcontext "github.com/tfcace/hash/internal/context"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/prediction"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/readline"
	"github.com/tfcace/hash/internal/shell/integration"
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
	agentHandler *AgentHandler
	responseUI   *ResponseUI
	history      *history.Store
	learning     *learning.FixStore
	clipboard    *clipboard.Buffer
	predictor    *prediction.Predictor
	suggestor    *CommandSuggestor
	colorPalette prompt.Palette
	allowlist    *allowlist.Manager
	agentOutput  *AgentOutputCoordinator

	// Conversation mode state
	conversation       *ConversationState
	convUI             *ConversationUI
	convCancelArmed    bool
	agentTimeoutCancel context.CancelFunc // Cancel per-request timeout when entering conversation
	// conversationInputHook allows tests to stub editor input reads.
	conversationInputHook func(context.Context) (string, error)

	lastExitCode int
	lastDuration time.Duration
	lastCommand  string // Last executed command
	lastStderr   string // Stderr from last command (truncated)
	lastCwd      string // Working directory of last command

	osc *integration.Emitter // OSC shell integration emitter

	// History navigation state for editor mode
	historyIndex     int    // -1 means current line (not in history)
	historySavedLine string // Saved current line when navigating into history

	// Context picker state
	selectedContext *hashcontext.Collection // nil = use auto-detect defaults

	// Directory change hook state
	prevCwd string // Previous working directory for chpwd hook
}

// New creates a new Shell instance.
//
//nolint:gocyclo // shell initialization wires up many subsystems sequentially
func New(cfg *config.Config) (*Shell, error) {
	e := executor.New()

	promptCfg := prompt.Config{
		Mode:         cfg.Prompt.Mode,
		StarshipPath: cfg.Prompt.StarshipPath,
		Alignment:    cfg.Prompt.Alignment,
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

	// Alias/function completer for user-defined functions
	router.Register(completion.NewAliasCompleter(e), completion.PriorityAlias)

	// Environment variable completer ($VAR, ${VAR})
	envCompleter := completion.NewEnvCompleter(e)
	envCompleter.SetMaskSensitive(cfg.Completions.MaskSensitiveEnv)
	router.Register(envCompleter, completion.PriorityEnv)

	// Executable completer for command names from PATH
	router.Register(completion.NewExecutableCompleter(), completion.PriorityExecutable)

	// Context-aware completions for git/jj branch and revision args.
	router.Register(completion.NewVCSCompleter(), completion.PriorityVCS)

	if cfg.Completions.CobraEnabled {
		router.Register(completion.NewCobraCompleter(), completion.PriorityToolNative)
	}

	// Set up agent (for both ?? commands and completions)
	var agentHandler *AgentHandler
	var agentClient *agent.Client

	// Select transport based on config
	var transport agent.Transport
	var acpTransport *agent.ACPTransport
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
			acpTransport = agent.NewACPTransport(agent.ACPConfig{
				Command: cfg.Agent.Command,
				Args:    cfg.Agent.Args,
			})
			transport = acpTransport
		}
	}

	// Create allowlist manager for agent permission requests
	// Reuse cwd from earlier in the function
	allowlistMgr := allowlist.New(
		cfg.Agent.AllowedCommandsScope,
		cwd,
		getConfigDir(),
	)

	// Create agent output coordinator for serialized output during agent interactions
	// Created early so permission handler can use it
	agentOutput := NewAgentOutputCoordinator(os.Stdout)

	// Wire up permission handler for ACP transport
	if acpTransport != nil {
		acpTransport.SetPermissionHandler(func(req agent.ToolPermissionRequest) (allow bool, always bool) {
			// Check allowlist first
			if allowlistMgr.IsAllowed(req.Command) {
				trace.AgentHigh("tool_permission", map[string]any{
					"command":  req.Command,
					"tool":     req.ToolName,
					"decision": "allowlist",
				})
				return true, false
			}

			trace.AgentHigh("tool_permission_prompt", map[string]any{
				"command": req.Command,
				"tool":    req.ToolName,
			})

			// Render permission prompt via coordinator (pauses streaming)
			agentOutput.RenderPermissionPrompt(req.Command, req.ToolName, colorPalette.Primary)

			// Flush stdout to ensure prompt is visible before reading input
			os.Stdout.Sync()

			// Read single keypress
			key := readSingleKey()

			allow, always = permissionDecisionForKey(key)
			decision := "deny"
			if allow {
				decision = "allow"
			}
			if always {
				decision = "always"
				allowlistMgr.Allow(req.Command) //nolint:errcheck
			}

			trace.AgentHigh("tool_permission", map[string]any{
				"command":  req.Command,
				"tool":     req.ToolName,
				"decision": decision,
				"key":      string(key),
			})
			agentOutput.ClearPermissionPrompt(allow)
			return allow, always
		})
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

	// Initialize prediction store and predictor
	var predictor *prediction.Predictor
	if cfg.Prediction.Enabled {
		predictionPath := filepath.Join(getDataDir(), "prediction.db")
		predictionStore, err := prediction.NewStore(predictionPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash: warning: prediction unavailable: %v\n", err)
		} else {
			predCfg := prediction.Config{
				Enabled:             cfg.Prediction.Enabled,
				AcceptKeys:          cfg.Prediction.AcceptKeys,
				ConfidenceThreshold: cfg.Prediction.ConfidenceThreshold,
				PathMinCount:        cfg.Prediction.PathMinCount,
				PathRecencyHours:    cfg.Prediction.PathRecencyHours,
			}
			predictor = prediction.NewPredictor(predictionStore, predCfg)
		}
	}

	// Initialize command suggestor (PATH caching happens in background)
	suggestor := NewCommandSuggestor(historyStore)

	// Initialize OSC shell integration emitter
	osc := integration.New()

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
			e.SetCaptureLimit(maxOutputSize)
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

	// Configure editor mode
	editorCfg := editor.Config{
		Keybindings:    cfg.Input.Keybindings,
		Gutter:         cfg.Input.Gutter,
		InputBgColor:   colorPalette.InputBg,
		ScrollbarColor: colorPalette.Primary,
		CompleteFunc:   makeEditorCompleteFunc(router),
		PrefetchFunc:   makeEditorPrefetchFunc(router),
		MaxPasteSize:   cfg.Input.ParseMaxPasteSize(),
	}

	// Capture initial working directory for chpwd hook
	initialCwd, _ := os.Getwd()

	shell := &Shell{
		config:       cfg,
		executor:     e,
		prompt:       p,
		readline:     rl,
		inputHandler: inputHandler,
		editorCfg:    editorCfg,
		agentHandler: agentHandler,
		responseUI:   NewResponseUI(os.Stdout),
		history:      historyStore,
		learning:     learningStore,
		clipboard:    clipboardBuf,
		predictor:    predictor,
		suggestor:    suggestor,
		colorPalette: colorPalette,
		allowlist:    allowlistMgr,
		agentOutput:  agentOutput,
		conversation: NewConversationState(),
		historyIndex: -1, // Start before history (current line)
		osc:          osc,
		prevCwd:      initialCwd,
	}

	// Set up history function for editor mode
	// This closure captures shell for proper state management
	if historyStore != nil {
		shell.editorCfg.HistoryFunc = shell.navigateHistory
	}

	// Set up shell integration callback for editor mode
	// This emits OSC 133;B (CommandStart) when the editor is ready for input
	shell.editorCfg.OnInputReady = func() {
		if shell.osc != nil {
			shell.osc.CommandStart()
		}
	}

	// Set prompt refresh callback for Ctrl+R history picker
	// This prints the Starship prefix (info bar) when returning from the picker
	inputHandler.SetPromptRefreshFunc(shell.printPromptPrefix)

	// Set accent color callback for permission prompts
	// This allows the permission prompt to use the dynamically-updated colorPalette
	shell.agentOutput.SetAccentColorFunc(func() string {
		return shell.colorPalette.Primary
	})

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
	defer s.readline.Close() //nolint:errcheck // cleanup on exit

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

	firstPrompt := true
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		s.emitShellIntegration(&firstPrompt)

		line, err := s.readLineWithEditor(ctx)
		trace.ShellDetailed("input_ready", map[string]any{
			"line":  line,
			"error": errStr(err),
		})

		if done, exitErr := s.handleInputError(err); done {
			return exitErr
		}

		line = trimSpace(line)
		if s.handleSpecialInput(line) {
			continue
		}

		if err := s.dispatchCommand(ctx, line); err == errExit {
			return nil
		}

		s.runChpwdHook(ctx)
		s.updatePrompt()
	}
}

// emitShellIntegration emits OSC shell integration sequences before prompt.
func (s *Shell) emitShellIntegration(firstPrompt *bool) {
	if s.osc == nil {
		return
	}
	if !*firstPrompt {
		s.osc.CommandFinished(s.lastExitCode)
	}
	*firstPrompt = false
	s.osc.PromptStart()
	if cwd, err := os.Getwd(); err == nil {
		s.osc.ReportDirectory(cwd)
	}
}

// handleInputError processes readline errors. Returns (done, error).
// done=true means exit the shell loop (with optional error).
// done=false means continue the loop (error is ignored).
func (s *Shell) handleInputError(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if readline.IsInterrupt(err) || errors.Is(err, ErrEditorCanceled) {
		// Ctrl+C at empty prompt: just print ^C and continue (like Fish)
		fmt.Println("^C")
		return false, nil
	}
	if readline.IsEOF(err) || errors.Is(err, ErrEditorEOF) {
		fmt.Println("exit")
		return true, nil
	}
	return true, err
}

// handleSpecialInput handles empty lines and !! shortcut. Returns true if handled.
func (s *Shell) handleSpecialInput(line string) bool {
	if line == "" {
		s.updatePrompt()
		return true
	}
	if line == "!!" {
		s.handleIssueShortcut()
		return true
	}
	return false
}

// handleIssueShortcut handles the !! shortcut for quick issue submission.
func (s *Shell) handleIssueShortcut() {
	if s.lastExitCode == 0 {
		fmt.Print("Last command succeeded. Open issue anyway? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.EqualFold(response, "y") {
			s.updatePrompt()
			return
		}
	}
	_ = s.builtinIssue([]string{"--last"})
	s.updatePrompt()
}

// dispatchCommand parses and executes a command line. Returns errExit if shell should exit.
func (s *Shell) dispatchCommand(ctx context.Context, line string) error {
	parsed := parser.Parse(line)
	trace.ShellHigh("dispatch", map[string]any{
		"type":    parsed.Type.String(),
		"command": parsed.Command,
		"prompt":  parsed.AgentPrompt,
	})

	switch parsed.Type {
	case parser.CommandTypeEmpty:
		s.updatePrompt()
	case parser.CommandTypeAgent, parser.CommandTypeAgentPipe, parser.CommandTypeAgentInline:
		if err := s.handleAgentRequest(ctx, parsed); err != nil {
			return err
		}
	case parser.CommandTypeRegular:
		return s.executeRegularCommand(ctx, line)
	}
	return nil
}

// executeRegularCommand executes a regular shell command.
func (s *Shell) executeRegularCommand(ctx context.Context, line string) error {
	// Check for builtins first
	handled, err := s.executeBuiltin(ctx, line)
	if err == errExit {
		return errExit
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		s.lastExitCode = 1
		s.updatePrompt()
		return nil
	}
	if handled {
		s.lastExitCode = 0
		s.updatePrompt()
		return nil
	}

	// Record command to clipboard buffer before execution
	if s.clipboard != nil {
		s.clipboard.AddCommand(line)
	}

	// Capture stderr for issue reporting
	stderrCap := newStderrCapture(os.Stderr)

	// Execute external command
	result, err := s.executor.Execute(ctx, line, os.Stdout, stderrCap)
	s.handleExecutionResult(line, result, err, stderrCap)
	return nil
}

// handleExecutionResult processes the result of command execution.
func (s *Shell) handleExecutionResult(line string, result *executor.Result, err error, stderrCap *stderrCapture) {
	if err != nil {
		s.handleExecutionError(err)
	} else {
		s.lastExitCode = result.ExitCode
		s.lastDuration = result.Duration
		if s.clipboard != nil && result.CapturedOutput != "" {
			s.clipboard.SetOutput(result.CapturedOutput)
		}
	}

	// Store the previous command for prediction before updating
	prevCommand := s.lastCommand

	// Store for issue reporting
	s.lastCommand = line
	s.lastStderr = stderrCap.String()
	s.lastCwd, _ = os.Getwd()

	// Record command in history
	s.recordCommand(line, s.lastExitCode, s.lastDuration)

	// Record command sequence for prediction (only successful commands)
	if s.predictor != nil && s.lastExitCode == 0 && prevCommand != "" {
		cwd, _ := os.Getwd()
		s.predictor.Record(prevCommand, line, cwd, nil)
	}
}

// handleExecutionError handles errors from command execution.
func (s *Shell) handleExecutionError(err error) {
	var cnf *executor.CommandNotFoundError
	if errors.As(err, &cnf) {
		suggestions := s.suggestor.Suggest(cnf.Command)
		installHint := s.suggestor.InstallHint(cnf.Command)
		if installHint == "" {
			for _, sug := range suggestions {
				if hint := s.suggestor.InstallHint(sug); hint != "" {
					installHint = hint
					break
				}
			}
		}
		handler := NewErrorHandler(s.learning)
		handler.HandleCommandNotFound(cnf.Command, suggestions, installHint)
		s.lastExitCode = 127
	} else {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		s.lastExitCode = 1
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
			handled, err := s.executeBuiltin(ctx, line)
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

// ErrEditorCanceled is returned when the editor is canceled (Ctrl+C).
var ErrEditorCanceled = errors.New("editor canceled")

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
		}

		// Set ghost text prediction based on last command
		if s.predictor != nil && s.lastCommand != "" {
			cwd, _ := os.Getwd()
			if predictedCmd := s.predictor.PredictCommand(s.lastCommand, cwd); predictedCmd != "" {
				ed.SetGhostText(predictedCmd)
			}
		}

		result, err := ed.Run(ctx)
		if err != nil {
			return "", err
		}
		if result.EOF {
			return "", ErrEditorEOF
		}
		if result.Canceled {
			return "", ErrEditorCanceled
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
		for i := range entries {
			commands[i] = entries[i].Command
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
		// Picker canceled or errored, keep existing selection
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

func (s *Shell) handleAgentRequest(ctx context.Context, parsed parser.ParseResult) error {
	// Show which mode was detected
	var modeLabel string
	switch parsed.Type {
	case parser.CommandTypeAgent:
		modeLabel = "command"
	case parser.CommandTypeAgentPipe:
		modeLabel = "pipe"
	case parser.CommandTypeAgentInline:
		modeLabel = "inline"
	}
	fmt.Fprintf(os.Stdout, "\033[90m[agent: %s]\033[0m ", modeLabel)

	if s.agentHandler == nil {
		fmt.Fprintf(os.Stderr, "\n\033[31m✗ Agent not configured.\033[0m\n")
		fmt.Fprintf(os.Stderr, "  Configure an agent in ~/.config/hash/config.toml:\n")
		fmt.Fprintf(os.Stderr, "  [agent]\n")
		fmt.Fprintf(os.Stderr, "  command = \"claude\"\n")
		fmt.Fprintf(os.Stderr, "  See docs/config-reference.md for options.\n")
		s.lastExitCode = 1
		return nil
	}

	// Create a new context for the agent request that we can cancel independently.
	// This allows Ctrl+C to cancel the agent operation without exiting the shell.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	defer agentCancel()

	// Set up SIGINT handler for this agent request
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// Handle SIGINT by canceling only the agent context
	go func() {
		select {
		case <-sigCh:
			s.agentOutput.Cancel()
			agentCancel()
		case <-agentCtx.Done():
		}
	}()

	// Pass selected context to agent handler
	s.agentHandler.SetSelectedContext(s.selectedContext)

	// Use unified streaming handler for all modes
	return s.handleAgentRequestUnified(agentCtx, parsed)
}

func (s *Shell) agentRequestTimeout() time.Duration {
	timeout := 30 * time.Second
	if s.config.Agent.Timeout != "" {
		if parsedDur, err := time.ParseDuration(s.config.Agent.Timeout); err == nil {
			timeout = parsedDur
		}
	}
	return timeout
}

// handleAgentInlineStreaming uses ghost text for inline completions.
func (s *Shell) handleAgentInlineStreaming(ctx context.Context, parsed parser.ParseResult, modelName string) error {
	requestCtx, timeoutCancel := context.WithTimeout(ctx, s.agentRequestTimeout())
	defer timeoutCancel()

	// Start streaming request
	textCh, errCh := s.agentHandler.StreamRequest(requestCtx, parsed)

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
	result, err := ed.Run(requestCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: editor error: %v\n", err)
		s.lastExitCode = 1
		return nil
	}

	if result.Canceled {
		return nil
	}

	if result.EOF {
		fmt.Println("exit")
		return errExit
	}

	// User accepted the command
	command := strings.TrimSpace(result.Text)
	if command == "" {
		return nil
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
	return nil
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
func (s *Shell) handleAgentRequestUnified(ctx context.Context, parsed parser.ParseResult) error {
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
			return nil
		}
		// Update context with pipe output
		if s.clipboard != nil {
			s.clipboard.AddCommand(parsed.Command)
			s.clipboard.SetOutput(pipeOutput)
		}
	}

	// Start progress bar
	s.responseUI.StartProgress()
	defer s.responseUI.StopProgress()

	// For inline mode, use editor with ghost text
	if parsed.Type == parser.CommandTypeAgentInline {
		return s.handleAgentInlineStreaming(ctx, parsed, modelName)
	}

	// Full ?? and pipe modes: streaming with confirmation UI
	s.handleAgentFullStreaming(ctx, parsed, modelName)
	return nil
}

// handleAgentFullStreaming handles full ?? and pipe modes with streaming.
//
//nolint:gocyclo // streaming loop handles markers, conversation detection, and error recovery
func (s *Shell) handleAgentFullStreaming(ctx context.Context, parsed parser.ParseResult, modelName string) {
	requestCtx, timeoutCancel := context.WithTimeout(ctx, s.agentRequestTimeout())
	s.agentTimeoutCancel = timeoutCancel
	defer func() {
		timeoutCancel()
		s.agentTimeoutCancel = nil
	}()

	// Show thinking indicator (multi-stage: thinking -> receiving)
	s.responseUI.ShowState(AgentStateThinking)

	// Start streaming request
	textCh, errCh := s.agentHandler.StreamRequest(requestCtx, parsed)

	// Collect streamed response with markdown rendering
	var response strings.Builder
	var streamErr error
	lineCount := 0 // Track lines for clearing on cancel
	renderer := markdown.NewStreamingRenderer()
	inConversation := false // Track if we detected conversation mode
	streamingStarted := false

	// Buffer for detecting conversation marker (may arrive in multiple chunks)
	var markerBuffer strings.Builder
	markerDetected := false
	const markerLen = len(agent.ConversationStartMarker) // "[CONVERSATION]" = 14 chars

	// Flush timer ensures partial lines appear on screen promptly.
	// The markdown renderer buffers text until \n; this timer flushes
	// incomplete lines so they're visible before events like permission
	// prompts steal the screen.
	const flushDelay = 50 * time.Millisecond
	flushTimer := time.NewTimer(flushDelay)
	flushTimer.Stop() // Don't fire until we have buffered content
	defer flushTimer.Stop()

collectLoop:
	for {
		select {
		case <-ctx.Done():
			s.responseUI.ClearLine()
			if inConversation {
				s.exitConversationMode()
			}
			fmt.Fprintln(os.Stderr, "hash: request canceled")
			s.lastExitCode = 1
			return
		case err, ok := <-errCh:
			if !ok {
				errCh = nil // Stop selecting on closed channel
				continue
			}
			if err != nil {
				streamErr = err
			}
		case <-flushTimer.C:
			// Flush partial line from markdown renderer so it appears on screen
			if partial := renderer.Flush(); partial != "" {
				if inConversation && s.convUI != nil {
					s.convUI.WriteStreamTinted(partial)
				} else {
					s.agentOutput.WriteStream(partial)
				}
			}
		case text, ok := <-textCh:
			if !ok {
				break collectLoop
			}

			// Start streaming UI on first chunk
			if !streamingStarted {
				streamingStarted = true
				s.agentOutput.StartStreaming()
				s.responseUI.ShowState(AgentStateReceiving)
				s.responseUI.ClearLine()
			}

			// Buffer initial text to detect conversation marker
			if !markerDetected {
				markerBuffer.WriteString(text)
				buffered := markerBuffer.String()

				// Check if we have enough to detect the marker
				if len(strings.TrimSpace(buffered)) >= markerLen || !strings.HasPrefix(strings.TrimSpace(agent.ConversationStartMarker), strings.TrimSpace(buffered)) {
					markerDetected = true

					if agent.HasConversationStart(buffered) {
						inConversation = true
						text = agent.StripConversationStart(buffered)
						s.setupConversationMode()
					} else {
						// Not a conversation - output buffered content
						text = buffered
					}
				} else {
					// Need more data to determine - continue buffering
					continue
				}
			}

			if inConversation {
				text = strings.ReplaceAll(text, agent.ConversationStartMarker, "")
				text = strings.ReplaceAll(text, agent.AwaitingInputMarker, "")
			}

			response.WriteString(text)
			// Count newlines for clearing on cancel
			lineCount += strings.Count(text, "\n")

			// Stream output with markdown rendering
			rendered := renderer.Write(text)
			if inConversation && s.convUI != nil {
				s.convUI.WriteStreamTinted(rendered)
			} else {
				s.agentOutput.WriteStream(rendered)
			}

			// Reset flush timer — if the renderer still has buffered content
			// (incomplete line), it will be flushed after the delay
			flushTimer.Reset(flushDelay)
		}
	}

	// Flush marker buffer if stream closed before detection completed.
	// This happens when the response is shorter than the marker length
	// or the connection drops mid-marker.
	if !markerDetected {
		if buffered := markerBuffer.String(); buffered != "" {
			if !streamingStarted {
				s.agentOutput.StartStreaming()
				s.responseUI.ShowState(AgentStateReceiving)
				s.responseUI.ClearLine()
			}
			if agent.HasConversationStart(buffered) {
				inConversation = true
				buffered = agent.StripConversationStart(buffered)
				s.setupConversationMode()
			}
			if buffered != "" {
				response.WriteString(buffered)
				lineCount += strings.Count(buffered, "\n")
				rendered := renderer.Write(buffered)
				if inConversation && s.convUI != nil {
					s.convUI.WriteStreamTinted(rendered)
				} else {
					s.agentOutput.WriteStream(rendered)
				}
			}
		}
	}

	// Flush any remaining buffered content from the renderer
	if remaining := renderer.Flush(); remaining != "" {
		if inConversation && s.convUI != nil {
			s.convUI.WriteStreamTinted(remaining)
		} else {
			s.agentOutput.WriteStream(remaining)
		}
	}

	// End streaming mode
	s.agentOutput.EndStreaming()

	// Handle error case first
	if streamErr != nil {
		if inConversation {
			s.exitConversationMode()
		}
		if s.handleAgentStreamError(ctx, parsed, modelName, streamErr, response.Len(), lineCount) {
			return
		}
	}

	// Success path - add newline after response and clear spinner
	if inConversation {
		fmt.Print("\x1b[K\x1b[0m\n") // Fill line, reset, newline
	} else {
		fmt.Println()
	}
	lineCount++
	s.responseUI.ClearLine() // Stop spinner

	// Check for conversation marker
	responseText := strings.TrimSpace(response.String())
	_, expectsInput := agent.ProcessAgentResponse(responseText)

	if expectsInput || inConversation {
		// Erase the [AWAITING_INPUT] marker from display if present
		if strings.HasSuffix(strings.TrimSpace(responseText), agent.AwaitingInputMarker) {
			fmt.Print("\x1b[1A\x1b[2K") // Move up, clear line
		}

		// If we already set up conversation mode during streaming, just run the loop
		if inConversation {
			s.conversation.SetSubState(ConversationAwaitingInput)
			s.runConversationLoop(ctx)
		} else {
			// Late detection - need to set up conversation mode now
			displayText, _ := agent.ProcessAgentResponse(responseText)
			s.enterConversationMode(ctx, displayText)
		}
		return
	}

	// Determine response type (single-turn flow)
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

	s.agentOutput.EnterConfirming()
	s.agentOutput.ShowHints(confirmType)
	action := s.responseUI.WaitForConfirmationByType(confirmType)
	s.agentOutput.ExitConfirming()
	fmt.Println()

	// Stop progress bar before any action
	s.responseUI.StopProgress()

	if s.handleAgentConfirmAction(ctx, action, confirmType, resp, responseText, lineCount) {
		return
	}
}

// handleAgentConfirmAction processes the user's confirmation choice.
// Returns true if the caller should return early (e.g., for edit mode).
func (s *Shell) handleAgentConfirmAction(ctx context.Context, action ConfirmAction, confirmType ConfirmationType, resp agent.Response, responseText string, lineCount int) bool {
	switch action {
	case ConfirmRun:
		if confirmType == ConfirmTypeCommand {
			s.executeAgentCommand(ctx, resp.Command)
		}
		// For explanations, ConfirmRun just dismisses
	case ConfirmEdit:
		if confirmType == ConfirmTypeCommand {
			s.handleEditCommand(ctx, resp.Command)
			return true
		}
		// Copy explanation to system clipboard
		if err := copyToSystemClipboard(responseText); err != nil {
			fmt.Fprintf(os.Stderr, "\033[90mCould not copy: %v\033[0m\n", err)
		} else {
			fmt.Fprintf(os.Stdout, "\033[90mCopied to clipboard\033[0m\n")
		}
	case ConfirmCancel:
		// Clear the streamed response from screen
		// +1 for confirmation hint line, +1 for the blank line after fmt.Println()
		s.responseUI.ClearLines(lineCount + 2)
	}
	return false
}

// executeAgentCommand executes a command generated by the agent.
func (s *Shell) executeAgentCommand(ctx context.Context, command string) {
	result, err := s.executor.Execute(ctx, command, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		s.lastExitCode = 1
	} else {
		s.lastExitCode = result.ExitCode
		s.lastDuration = result.Duration
	}
	s.recordCommand(command, s.lastExitCode, s.lastDuration)
}

// handleAgentStreamError handles errors during agent streaming.
// Returns true if the caller should return (error was fully handled).
func (s *Shell) handleAgentStreamError(ctx context.Context, parsed parser.ParseResult, modelName string, streamErr error, responseLen, lineCount int) bool {
	s.responseUI.ClearLine() // Stop spinner and clear the line
	s.responseUI.ShowError(streamErr.Error())

	// If no response was received, this is a startup/connection failure
	// (e.g., agent not in PATH). Just return to prompt - retry won't help.
	if responseLen == 0 {
		s.responseUI.ShowAgentHint(
			s.config.Agent.Transport,
			s.config.Agent.Command,
			s.config.Agent.URL,
		)
		s.lastExitCode = 1
		return true
	}

	// Mid-stream error with partial response - offer retry
	s.agentOutput.EnterConfirming()
	s.agentOutput.ShowHints(ConfirmTypeError)
	action := s.responseUI.WaitForConfirmationByType(ConfirmTypeError)
	s.agentOutput.ExitConfirming()
	fmt.Println()
	if action == ConfirmRun { // Retry
		s.handleAgentFullStreaming(ctx, parsed, modelName)
		return true
	}
	// Cancel: clear error + any partial response
	// lineCount + error line + confirmation line + blank line
	s.responseUI.ClearLines(lineCount + 3)
	return true
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
		return
	}

	if result.Canceled || result.EOF {
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

func makeEditorPrefetchFunc(router *completion.Router) func(string, int) {
	return func(line string, pos int) {
		router.Prefetch(line, pos)
	}
}

// Close releases shell resources.
func (s *Shell) Close() error {
	if s.history != nil {
		_ = s.history.Close()
	}
	if s.learning != nil {
		_ = s.learning.Close()
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

	_, _ = s.history.Add(cmd)
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

// getConfigDir returns the config directory for hash.
func getConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hash")
}

// History returns the history store for use by builtins.
func (s *Shell) History() *history.Store {
	return s.history
}

// Learning returns the learning store for use by builtins.
func (s *Shell) Learning() *learning.FixStore {
	return s.learning
}

// setupConversationMode initializes conversation UI for streaming.
// Called when [CONVERSATION] marker is detected at the start of a response.
// The content will be streamed directly with tinting - no re-rendering needed.
func (s *Shell) setupConversationMode() {
	accentColor := "#7c3aed" // Default purple
	if s.colorPalette.Primary != "" {
		accentColor = s.colorPalette.Primary
	}
	s.convUI = NewConversationUI(os.Stdout, accentColor)
	s.agentOutput.SetStreamTint(s.convUI.TintBg())
	s.agentOutput.SetStreamBorder(s.convUI.StreamBorder())
	s.conversation.Activate()
	s.conversation.SetSubState(ConversationStreaming)
	s.convCancelArmed = false

	// Write the top border to start the visual frame
	s.convUI.WriteTopBorder()
}

// enterConversationMode enters multi-turn conversation with the agent.
// Used for late detection (when [CONVERSATION] wasn't at the start).
func (s *Shell) enterConversationMode(ctx context.Context, initialResponse string) {
	// Create conversation UI with accent color
	accentColor := "#7c3aed" // Default purple
	if s.colorPalette.Primary != "" {
		accentColor = s.colorPalette.Primary
	}
	s.convUI = NewConversationUI(os.Stdout, accentColor)
	s.agentOutput.SetStreamTint(s.convUI.TintBg())
	s.agentOutput.SetStreamBorder(s.convUI.StreamBorder())
	s.conversation.Activate()
	s.convCancelArmed = false

	// Clear the un-framed response that was already streamed
	// Count lines in the response to know how many to clear
	lineCount := strings.Count(initialResponse, "\n") + 1
	for i := 0; i < lineCount; i++ {
		fmt.Print("\x1b[1A\x1b[2K") // Move up, clear line
	}

	// Now re-render the response within the conversation frame
	s.convUI.WriteTopBorder()
	// Re-render the initial response with framing
	s.convUI.WriteStreamTinted(initialResponse)
	fmt.Print("\x1b[K\x1b[0m\n") // Fill and newline

	s.conversation.SetSubState(ConversationAwaitingInput)

	// Conversation input loop
	s.runConversationLoop(ctx)
}

// runConversationLoop handles the conversation input loop.
func (s *Shell) runConversationLoop(ctx context.Context) {
	// Cancel the per-request timeout — conversation mode is interactive and
	// should not be killed by the initial 30s agent timeout. Each individual
	// reply still has its own idle timeout via SendStreaming.
	if s.agentTimeoutCancel != nil {
		s.agentTimeoutCancel()
		s.agentTimeoutCancel = nil
	}

	// Parse conversation idle timeout from config
	idleTimeout := 10 * time.Minute // default
	if s.config.Agent.ConversationIdleTimeout != "" {
		if parsed, err := time.ParseDuration(s.config.Agent.ConversationIdleTimeout); err == nil {
			idleTimeout = parsed
		}
	}

	for s.conversation.IsActive() {
		// Apply idle timeout per input read — if the user doesn't respond
		// within the configured duration, exit conversation mode.
		// A zero or negative timeout disables the idle timeout.
		var inputCtx context.Context
		var inputCancel context.CancelFunc
		if idleTimeout > 0 {
			inputCtx, inputCancel = context.WithTimeout(ctx, idleTimeout)
		} else {
			inputCtx, inputCancel = context.WithCancel(ctx)
		}
		line, err := s.readConversationInputWithHook(inputCtx)
		inputCancel()

		if err != nil {
			// Check if the error was caused by the idle timeout
			if inputCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				if s.convUI != nil {
					s.convUI.WriteIdleTimeout()
				}
				s.exitConversationMode()
				return
			}
			if err == ErrEditorCanceled {
				if s.convCancelArmed {
					s.convCancelArmed = false
					s.exitConversationMode()
					return
				}
				s.convCancelArmed = true
				if s.convUI != nil {
					s.convUI.WriteCancelHint()
				}
				continue
			}
			// Ctrl+D or other error - exit conversation mode
			s.exitConversationMode()
			return
		}

		s.convCancelArmed = false
		input := strings.TrimSpace(line)

		// Handle explicit exit commands (/done, /exit, /quit)
		if s.conversation.IsExitCommand(input) {
			s.exitConversationMode()
			return
		}

		// Handle shell escape
		if s.conversation.IsShellEscape(input) {
			cmd := s.conversation.ExtractShellCommand(input)
			s.executeShellEscape(ctx, cmd)
			continue
		}

		// Send reply to agent
		s.sendConversationReply(ctx, input)
	}
}

func (s *Shell) readConversationInputWithHook(ctx context.Context) (string, error) {
	if s.conversationInputHook != nil {
		return s.conversationInputHook(ctx)
	}
	return s.readConversationInput(ctx)
}

// readConversationInput reads a reply inside the conversation user box.
func (s *Shell) readConversationInput(ctx context.Context) (string, error) {
	cfg := s.editorCfg
	cfg.Gutter = false
	cfg.Prompt = ""
	cfg.HistoryFunc = nil
	cfg.CompleteFunc = nil
	cfg.PrefetchFunc = nil
	cfg.OnInputReady = nil
	cfg.DisableLineContinuation = true
	cfg.PreventEmptySubmit = true
	cfg.DisableHistorySearch = true
	cfg.DisableContextPicker = true
	cfg.ClearOnCancel = true
	if s.convUI != nil {
		cfg.InputFrame = s.convUI.InputFrame()
	}

	ed := editor.New(cfg, os.Stdin, os.Stdout)
	result, err := ed.Run(ctx)
	if err != nil {
		return "", err
	}
	if result.EOF {
		return "", ErrEditorEOF
	}
	if result.Canceled {
		return "", ErrEditorCanceled
	}
	return result.Text, nil
}

// exitConversationMode exits conversation mode cleanly.
func (s *Shell) exitConversationMode() {
	s.conversation.Deactivate()
	if s.convUI != nil {
		s.convUI.WriteBottomBorder() // Close the visual frame
		s.convUI.ClearTint()
	}
	s.agentOutput.ClearStreamStyle()
	fmt.Println() // Clean line

	// Restore normal shell prompt
	s.readline.SetPrompt(s.currentPromptLine())
}

// executeShellEscape runs a shell command within conversation mode.
func (s *Shell) executeShellEscape(ctx context.Context, cmd string) {
	s.conversation.SetSubState(ConversationExecutingShell)

	// Execute command
	result, err := s.executor.Execute(ctx, cmd, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
	} else {
		s.lastExitCode = result.ExitCode
	}

	// Return to awaiting input (readline will show prompt)
	s.conversation.SetSubState(ConversationAwaitingInput)
}

// sendConversationReply sends a follow-up message to the agent.
//
//nolint:gocyclo // conversation reply handles marker buffering, spinner lifecycle, and stream state
func (s *Shell) sendConversationReply(ctx context.Context, reply string) {
	s.conversation.SetSubState(ConversationStreaming)

	// Build request with reply as prompt
	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: reply,
	}

	// Start conversation-mode spinner (tinted)
	spinnerCtx, spinnerCancel := context.WithCancel(ctx)
	defer spinnerCancel() // Ensure context is always released
	spinnerDone := make(chan struct{})
	go s.runConversationSpinner(spinnerCtx, spinnerDone)

	// Stream request
	textCh, errCh := s.agentHandler.StreamRequest(ctx, parsed)

	// Collect and display response
	var response strings.Builder
	renderer := markdown.NewStreamingRenderer()
	spinnerStopped := false

	// Buffer for detecting/stripping conversation marker in follow-ups
	var markerBuffer strings.Builder
	markerStripped := false
	const markerLen = len(agent.ConversationStartMarker)

collectLoop:
	for {
		select {
		case <-ctx.Done():
			spinnerCancel()
			<-spinnerDone
			s.convUI.ClearThinkingIndicator()
			s.conversation.SetSubState(ConversationAwaitingInput)
			return
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				spinnerCancel()
				<-spinnerDone
				s.convUI.ClearThinkingIndicator()
				s.responseUI.ShowError(err.Error())
				s.conversation.SetSubState(ConversationAwaitingInput)
				return
			}
		case text, ok := <-textCh:
			if !ok {
				break collectLoop
			}
			if response.Len() == 0 && !spinnerStopped {
				// First chunk - stop spinner and clear line
				spinnerCancel()
				<-spinnerDone
				s.convUI.ClearThinkingIndicator()
				spinnerStopped = true
			}

			// Buffer initial text to detect and strip conversation marker
			if !markerStripped {
				markerBuffer.WriteString(text)
				buffered := markerBuffer.String()

				// Check if we have enough to detect the marker
				if len(strings.TrimSpace(buffered)) >= markerLen || !strings.HasPrefix(strings.TrimSpace(agent.ConversationStartMarker), strings.TrimSpace(buffered)) {
					markerStripped = true
					if agent.HasConversationStart(buffered) {
						text = agent.StripConversationStart(buffered)
					} else {
						text = buffered
					}
				} else {
					continue // Need more data
				}
			}

			if s.conversation.IsActive() {
				text = strings.ReplaceAll(text, agent.ConversationStartMarker, "")
			}

			response.WriteString(text)
			// Strip [AWAITING_INPUT] from rendered output to avoid visual
			// artifacts. Keep it in response for ProcessAgentResponse detection.
			renderText := strings.ReplaceAll(text, agent.AwaitingInputMarker, "")
			rendered := renderer.Write(renderText)
			s.convUI.WriteStreamTinted(rendered) // Stream with conversation tint
		}
	}

	// Flush marker buffer if stream closed before detection completed
	if !markerStripped {
		if buffered := markerBuffer.String(); buffered != "" {
			if !spinnerStopped {
				spinnerCancel()
				<-spinnerDone
				s.convUI.ClearThinkingIndicator()
				spinnerStopped = true
			}
			if agent.HasConversationStart(buffered) {
				buffered = agent.StripConversationStart(buffered)
			}
			if buffered != "" {
				response.WriteString(buffered)
				rendered := renderer.Write(buffered)
				s.convUI.WriteStreamTinted(rendered)
			}
		}
	}

	// Stop spinner if not already stopped
	if !spinnerStopped {
		spinnerCancel()
		<-spinnerDone
		s.convUI.ClearThinkingIndicator()
	}

	// Flush remaining
	if remaining := renderer.Flush(); remaining != "" {
		s.convUI.WriteStreamTinted(remaining)
	}
	fmt.Print("\x1b[K\x1b[0m\n") // Fill line, reset tint, newline

	// Check for another follow-up
	responseText := strings.TrimSpace(response.String())
	_, expectsInput := agent.ProcessAgentResponse(responseText)

	if expectsInput {
		// Continue conversation ([AWAITING_INPUT] stripped during rendering)
		s.conversation.SetSubState(ConversationAwaitingInput)
	} else {
		// Agent done, exit conversation mode
		s.exitConversationMode()
	}
}

// runConversationSpinner runs an animated spinner within the conversation zone.
func (s *Shell) runConversationSpinner(ctx context.Context, done chan struct{}) {
	defer close(done)

	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	frame := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.convUI.WriteThinkingIndicator(frames[frame%len(frames)], "Agent thinking...")
			frame++
		}
	}
}

// runChpwdHook runs configured chpwd hook commands if the working directory changed.
func (s *Shell) runChpwdHook(ctx context.Context) {
	if s.config == nil || len(s.config.Shell.Hooks.Chpwd) == 0 {
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	// Resolve symlinks for comparison (macOS has /var -> /private/var)
	cwdResolved, _ := filepath.EvalSymlinks(cwd)
	prevResolved, _ := filepath.EvalSymlinks(s.prevCwd)
	if cwdResolved == prevResolved {
		return
	}

	s.prevCwd = cwd
	for _, cmd := range s.config.Shell.Hooks.Chpwd {
		_, err := s.executor.Execute(ctx, cmd, nil, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash: chpwd hook failed: %v\n", err)
		}
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// permissionDecisionForKey maps permission prompt key presses to decisions.
func permissionDecisionForKey(key byte) (allow, always bool) {
	switch key {
	case 'y', 'Y', '\r', '\n':
		return true, false
	case 'a', 'A':
		return true, true
	default:
		return false, false
	}
}

// readSingleKey reads a single keypress from stdin.
// Returns the key byte (handles escape sequences for special keys).
func readSingleKey() byte {
	fd := int(os.Stdin.Fd())

	// Put terminal in raw mode to read single character
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 'n' // Default to deny on error
	}
	defer term.Restore(fd, oldState)

	// Drain any stale input (e.g., lingering newline from Enter that
	// submitted the original command) so it doesn't auto-answer the prompt.
	drainStdin(fd)

	// Read into a larger buffer so that multi-byte escape sequences
	// (e.g., arrow keys: \x1b[A) are consumed in one read rather than
	// leaving trailing bytes in stdin for subsequent reads.
	buf := make([]byte, 16)
	n, err := os.Stdin.Read(buf)
	if err != nil || n < 1 {
		return 'n'
	}

	char := buf[0] //nolint:gosec // n >= 1 guarantees buf[0] is valid
	if char == 0x1b {
		return 0x1b // Return ESC (sequence bytes already consumed)
	}

	return char
}

// drainStdin discards any bytes already buffered in stdin.
// Uses non-blocking reads so it returns immediately when the buffer is empty.
func drainStdin(fd int) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return
	}
	defer syscall.SetNonblock(fd, false) //nolint:errcheck
	discard := make([]byte, 256)
	for {
		if _, err := os.Stdin.Read(discard); err != nil {
			return
		}
	}
}
