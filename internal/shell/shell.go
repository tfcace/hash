package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
	"mvdan.cc/sh/v3/syntax"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/allowlist"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/config"
	hashcontext "github.com/tfcace/hash/internal/context"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/onboarding"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/prediction"
	"github.com/tfcace/hash/internal/programstatus"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/shell/integration"
	"github.com/tfcace/hash/internal/shparse"
	"github.com/tfcace/hash/internal/trace"
	"github.com/tfcace/hash/internal/version"
)

const editorCompletionTimeout = 150 * time.Millisecond

// Mode represents the shell's startup mode.
type Mode struct {
	Login       bool // Login shell (sources profile files)
	Interactive bool // Interactive shell (has TTY)
}

// Shell is the main Hash shell instance.
type Shell struct {
	mode                  Mode     // Startup mode
	stdin, stdout, stderr *os.File // the shell's own terminal streams; nil: the process's (in, out, errOut)
	config                *config.Config
	executor              *executor.Executor
	prompt                *prompt.Prompt
	editorCfg             editor.Config // Editor configuration
	agentHandler          *AgentHandler
	responseUI            *ResponseUI
	history               *history.Store
	historyPath           string
	learning              *learning.FixStore
	fixes                 *fixTracker     // Learning loop: observes outcomes, suggests fixes
	errors                *ErrorHandler   // Renders error/fix banners (stderr by default)
	branchLister          func() []string // Local git branches for did-you-mean; nil = gitBranches
	clipboard             *clipboard.Buffer
	predictor             *prediction.Predictor
	suggestor             *CommandSuggestor
	colorPalette          prompt.Palette
	allowlist             *allowlist.Manager
	agentOutput           *AgentOutputCoordinator
	agentStatus           *agentStatus                                     // OSC 7501 reports for the ?? turn
	interrupts            interrupts                                       // routes Ctrl+C (SIGINT) to whatever claims it; hash itself never dies of it
	turn                  atomic.Pointer[liveTurn]                         // the ?? turn in flight, nil between turns
	updateNotices         chan agentupdate.Notice                          // newer-adapter notices from the background check, drained before a prompt
	offer                 promptOffer                                      // ghost text offered at the next prompt only
	updateChecker         func(context.Context) (agentupdate.Notice, bool) // nil = real detect + registry check
	adapterVersion        func() string                                    // nil = re-detect the installed adapter; tests inject a version
	installer             adapterInstaller                                 // nil = agentupdate.Installer for the configured command
	updateStateFile       string                                           // override of agentUpdateStatePath() for tests
	latestLookup          func(context.Context) string                     // nil = ask the npm registry; tests inject a version
	status                lineStatus                                       // how the user's last line ended
	last                  commandOutcome                                   // the user's last command, a shell line or ?? request

	marks promptMarks // OSC 133 marks and OSC 7 for each prompt

	// History navigation state for editor mode
	historyNav historyNav

	// Context picker state
	contextPicks hashcontext.Selection // what Ctrl+P picked; the zero value is the auto-detected defaults
	exitStatus   int                   // the status the shell exits with: Run's end sets it (ended), close sets it last (the EXIT trap may exit)

	// Directory change hook state
	prevCwd string // Previous working directory for chpwd hook

	// Completion plugin state (nil when completions.plugins_enabled = false)
	pluginCompleter *completion.PluginCompleter
}

// New creates a new Shell instance.
//
//nolint:gocyclo // shell initialization wires up many subsystems sequentially
func New(cfg *config.Config) (*Shell, error) {
	e := executor.New()
	if err := e.SetDialect(cfg.Shell.Dialect); err != nil {
		fmt.Fprintf(os.Stderr, "hash: warning: %v; using bash dialect\n", err)
	}
	UseSourceFilter(e)
	agentCfg := cfg.EffectiveAgent()

	promptCfg := prompt.Config{
		Mode:         cfg.Prompt.Mode,
		StarshipPath: cfg.Prompt.StarshipPath,
		Alignment:    cfg.Prompt.Alignment,
	}
	p := prompt.New(promptCfg)

	// Use default palette initially — will be extracted from starship in Run()
	colorPalette := prompt.DefaultPalette()

	// Set up completion router
	router := completion.NewRouter()
	router.SetFuzzy(cfg.Completions.Fuzzy)
	_ = router.SetDialect(e.Dialect()) // completed lines are read back as the executor parses them

	fileCompleter := completion.NewFileCompleter()
	fileCompleter.SetFuzzyMode(cfg.Completions.Fuzzy)
	router.Register(fileCompleter, completion.PriorityFilesystem)

	// Environment variable completer ($VAR, ${VAR})
	envCompleter := completion.NewEnvCompleter(e)
	envCompleter.SetMaskSensitive(cfg.Completions.MaskSensitiveEnv)
	router.Register(envCompleter, completion.PriorityEnv)

	// Executable completer for command names from PATH, plus the user's
	// functions and aliases and hash builtins.
	executableCompleter := completion.NewExecutableCompleter()
	executableCompleter.SetFunctions(e)
	executableCompleter.SetBuiltins(enabledCompletableBuiltins(cfg))
	router.Register(executableCompleter, completion.PriorityExecutable)

	// Context-aware completions for git/jj branch and revision args.
	router.Register(completion.NewVCSCompleter(), completion.PriorityVCS)

	// Semantic completions for common commands (ssh, make, kill, npm, etc.)
	router.Register(completion.NewSemanticCompleter(), completion.PrioritySemantic)

	completionReadyCh := make(chan struct{}, 1)

	// Declarative completion plugins: built-in specs (docker) plus user specs
	// from <config>/completions/*.toml. PriorityPlugin outranks the semantic
	// and tool-native completers, so user plugins override the built-in
	// handlers; registration order does not matter.
	var pluginCompleter *completion.PluginCompleter
	if cfg.Completions.PluginsEnabled {
		pluginSpecs, pluginErrs := completion.LoadPluginSpecs(filepath.Join(getConfigDir(), "completions"))
		for _, pluginErr := range pluginErrs {
			fmt.Fprintf(os.Stderr, "hash: warning: completion plugin: %v\n", pluginErr)
		}
		pluginCompleter = completion.NewPluginCompleter(pluginSpecs)
		pluginCompleter.SetFuzzyMode(cfg.Completions.Fuzzy)
		router.Register(pluginCompleter, completion.PriorityPlugin)
	}

	if cfg.Completions.CobraEnabled {
		router.Register(completion.NewCobraCompleter(cfg.Completions.CobraCommands...), completion.PriorityToolNative)
	}

	// Wake the editor when a slow source (plugin, cobra, git, PATH scan,
	// ...) lands so a "fetching" menu can fill itself in without the user
	// pressing Tab again. Non-blocking: one pending wake-up is enough to
	// trigger a refresh.
	router.SetOnReady(func() {
		select {
		case completionReadyCh <- struct{}{}:
		default:
		}
	})

	// Set up agent (for both ?? commands and completions)
	var agentHandler *AgentHandler
	var agentClient *agent.Client

	// Select transport based on config
	var transport agent.Transport
	var acpTransport *agent.ACPTransport
	switch agentCfg.Transport {
	case "http":
		if agentCfg.URL != "" {
			transport = agent.NewHTTPTransport(agent.HTTPConfig{
				URL:     agentCfg.URL,
				Model:   agentCfg.Model,
				Headers: agentCfg.Headers,
			})
		}
	default: // "stdio" or "acp" or unset - use ACP protocol
		if agentCfg.Command != "" {
			acpTransport = agent.NewACPTransport(agent.ACPConfig{
				Command: agentCfg.Command,
				Args:    agentCfg.Args,
			})
			transport = acpTransport
		}
	}

	// Create allowlist manager for agent permission requests
	cwd, _ := os.Getwd()
	scope, scopeErr := allowlist.ParseScope(agentCfg.AllowedCommandsScope)
	if scopeErr != nil {
		fmt.Fprintf(os.Stderr, "hash: %v; approvals last for this session only\n", scopeErr)
	}
	allowlistMgr := allowlist.New(scope, cwd, getConfigDir())

	// Create agent output coordinator for serialized output during agent interactions
	// Created early so permission handler can use it
	agentOutput := NewAgentOutputCoordinator(os.Stdout)

	if transport != nil {
		agentClient = agent.NewClient(transport)
		agentHandler = NewAgentHandler(agentClient)
	}

	// Initialize history store
	var historyStore *history.Store
	historyPath := configuredHistoryPath(cfg)
	var err error
	if cfg.History.Enabled {
		historyStore, err = history.NewStore(historyPath)
		if err != nil {
			// Log warning but don't fail - history is optional
			fmt.Fprintf(os.Stderr, "hash: warning: history unavailable: %v\n", err)
		}
	}

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
				RecencyWindowHours:  cfg.Prediction.RecencyWindowHours,
			}
			predictor = prediction.NewPredictor(predictionStore, predCfg)
		}
	}

	// Initialize command suggestor (PATH caching happens in background).
	// A typo is looked up in the PATH commands run with, which rc files set.
	suggestor := NewCommandSuggestor(historyStore)
	suggestor.pathFunc = func() string { return environValue(e.Environ(), "PATH") }

	// Initialize OSC shell integration emitter
	osc := integration.New()

	// Program status (OSC 7501) for the ?? turn, on a terminal only
	statusReporter := programstatus.NewReporter(os.Stdout)
	statusReporter.SetEnabled(term.IsTerminal(int(os.Stdout.Fd())))

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

	// Set clipboard buffer on agent handler for context
	if agentHandler != nil {
		agentHandler.SetClipboard(clipboardBuf)
	}

	// Configure editor mode
	editorCfg := editor.Config{
		Gutter:              cfg.Input.Gutter,
		InputBgColor:        "",
		ScrollbarColor:      colorPalette.Primary,
		CompleteOutcomeFunc: makeEditorCompleteOutcomeFunc(router),
		CompletionReadyCh:   completionReadyCh,
		PrefetchFunc:        makeEditorPrefetchFunc(router),
		SuggestionFunc:      makeEditorSuggestionFunc(historyStore, predictor),
		// The shell's one key reader, for its life (see keyboard).
		Input: newKeyReader(os.Stdin, cfg.Input.ParseMaxPasteSize()),
	}

	// Capture initial working directory for chpwd hook
	initialCwd, _ := os.Getwd()

	shell := &Shell{
		config:       cfg,
		executor:     e,
		prompt:       p,
		editorCfg:    editorCfg,
		agentHandler: agentHandler,
		responseUI:   NewResponseUI(os.Stdout),
		agentStatus:  newAgentStatus(statusReporter),
		history:      historyStore,
		historyPath:  historyPath,
		learning:     learningStore,
		fixes:        newFixTracker(learningStore),
		errors:       NewErrorHandler(),
		clipboard:    clipboardBuf,
		predictor:    predictor,
		suggestor:    suggestor,
		colorPalette: colorPalette,
		allowlist:    allowlistMgr,
		agentOutput:  agentOutput,
		marks:        promptMarks{osc: osc},
		prevCwd:      initialCwd,

		pluginCompleter: pluginCompleter,
	}
	shell.registerExecutorBuiltins()

	if acpTransport != nil {
		acpTransport.SetPermissionHandler(shell.handleToolPermission)
	}
	// Set up history function for editor mode
	// This closure captures shell for proper state management
	if historyStore != nil {
		shell.editorCfg.HistoryFunc = shell.navigateHistory
	}

	// Tab on an inline ?? line submits it to start agent completion
	shell.editorCfg.AgentCompleteLine = shell.agentCompleteLinePredicate(exec.LookPath)
	// Enter on an unfinished shell line adds a row, as at bash's PS2
	shell.editorCfg.Incomplete = shell.lineIncomplete

	// Set up shell integration callback for editor mode
	// This emits OSC 133;B (CommandStart) when the editor is ready for input
	shell.editorCfg.OnInputReady = shell.marks.inputReady

	// Set accent color callback for permission prompts
	// This allows the permission prompt to use the dynamically-updated colorPalette
	shell.agentOutput.SetAccentColorFunc(func() string {
		return shell.colorPalette.Primary
	})

	// A refused command (not found, not executable) gets its banner at the
	// point of failure while the line keeps running with the right status.
	// The banner is all: the status is the line's, and a hook's refusal is
	// not the user's line.
	e.SetRefusalHandler(func(err error) { shell.showRefusal(err) })

	return shell, nil
}

// NewWithMode creates a new Shell instance with explicit mode.
func NewWithMode(cfg *config.Config, mode Mode) (*Shell, error) {
	sh, err := New(cfg)
	if err != nil {
		return nil, err
	}
	sh.mode = mode
	if mode.Interactive {
		sh.executor.UseInteractiveTerminal()
	}
	return sh, nil
}

// Mode returns the shell's current mode.
func (s *Shell) Mode() Mode {
	return s.mode
}

// Run starts the shell REPL.
func (s *Shell) Run(ctx context.Context) error {
	// Hash owns SIGINT from here on: without a handler, a Ctrl+C that reaches
	// hash (a command running with the terminal cooked, a builtin waiting on
	// the agent) would be Go's default and end the shell.
	defer s.interrupts.listen()()

	// Run startup files and commands based on mode
	if err := s.runStartup(ctx); err != nil {
		return s.ended(err)
	}
	s.showWelcomeIfNeeded()

	// Generate first prompt and extract color palette concurrently.
	// This replaces separate refreshColorPalette() + printPromptPrefix() calls,
	// reducing starship subprocess spawns from 4 to 2 (~75ms savings).
	first := s.initPromptAndPalette()
	head := &first // the first prompt's info bar, drawn once it starts
	s.startUpdateCheck(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		s.marks.promptStart(s.status.exitCode)
		s.drawPromptHead(head)
		head = nil
		trace.ShellHigh("prompt_start", map[string]any{
			"mode": "editor",
		})

		line, err := s.readLineWithEditor(ctx)
		s.retirePromptGhosts()
		trace.ShellDetailed("input_ready", map[string]any{
			"line":  line,
			"error": errStr(err),
		})

		if stop := s.handleInputError(err); stop != nil {
			return s.ended(stop)
		}

		if err := s.handleLine(ctx, trimSpace(line)); err != nil {
			return s.ended(err)
		}
	}
}

// shellExit is the shell ending with the status hash exits with: exit ran
// (on a line, in an agent command, in init_commands), or the input ended
// (Ctrl+D). It is the error the line returns, up to Run.
type shellExit struct{ status int }

func (e shellExit) Error() string { return fmt.Sprintf("exit %d", e.status) }

// endsShell reports whether err is the shell ending.
func endsShell(err error) bool {
	var exit shellExit
	return errors.As(err, &exit)
}

// ended is what Run returns for err, the error that stopped its loop: a
// shellExit becomes the status ExitStatus reports, and $? with it, so the
// EXIT trap Close runs sees the status the shell exits with (exit 4 after a
// failure is 4, as in bash). Any other error is Run's.
func (s *Shell) ended(err error) error {
	var exit shellExit
	if errors.As(err, &exit) {
		s.exitStatus = exit.status
		s.syncStatus(exit.status)
		return nil
	}
	return err
}

// handleLine runs one line the user entered. It returns a shellExit when the line ended the shell, and ctx's
// error when the shell is ending (a fatal signal ended ctx while the line
// ran).
//
// Once the line and its hooks have run, $? is set to the line's status, the
// one the prompt and OSC 133 D show: this is the only place it is set for a
// line, so a hook, a ?? request or a builtin hash runs itself cannot leave
// the interpreter disagreeing with s.status. An empty line keeps $? as it is.
func (s *Shell) handleLine(ctx context.Context, line string) error {
	if line == "" {
		return nil
	}
	s.marks.lineRan()
	if line == "!!" {
		s.handleIssueShortcut(ctx)
	} else if err := s.dispatchCommand(ctx, line); endsShell(err) {
		return err
	}
	if err := ctx.Err(); err != nil {
		// The shell is ending (a fatal signal): no hook runs, and no prompt
		// bar draws for a prompt that never comes.
		s.syncStatus(s.status.exitCode)
		return err
	}
	s.runChpwdHook(ctx)
	s.syncStatus(s.status.exitCode)
	return nil
}

func (s *Shell) showWelcomeIfNeeded() {
	if !s.mode.Interactive {
		return
	}
	welcome := NewWelcome(getConfigDir())
	if !welcome.ShouldShow() {
		return
	}
	s.runOnboarding()
	_ = welcome.MarkShown()
}

// runOnboarding shows the agent-aware first-run panel: detect adapters, let
// the user pick one (writing config.toml) or skip, with the quick-start tips
// folded in. Re-runnable via the `setup` builtin.
func (s *Shell) runOnboarding() {
	if !s.mode.Interactive {
		return
	}
	configPath := filepath.Join(getConfigDir(), "config.toml")
	agents := onboarding.Detect(exec.LookPath)
	chosen, ok, err := onboarding.New(agents, version.Version).Run()
	if err != nil {
		return
	}

	if !ok {
		fmt.Fprintf(s.out(), "  No agent set up. Run %s anytime · %s\n", "\033[36msetup\033[0m", onboardingDocsURL)
		return
	}

	switch {
	case onboarding.AgentConfigured(configPath):
		fmt.Fprintf(s.out(), "  Config already exists at %s — left unchanged.\n", configPath)
	default:
		if werr := onboarding.WriteAgentConfig(configPath, chosen); werr != nil {
			fmt.Fprintf(s.errOut(), "  Could not write config: %v\n", werr)
			return
		}
		// The default config already targets claude-agent-acp, so when the
		// chosen command matches what's loaded and a handler exists, ?? works
		// immediately; otherwise the new transport applies on next launch.
		if s.agentHandler != nil && s.config.EffectiveAgent().Command == chosen.Command {
			fmt.Fprintf(s.out(), "  \033[32m✓\033[0m %s ready — type \033[36m??\033[0m to try it.\n", chosen.Name)
		} else {
			fmt.Fprintf(s.out(), "  \033[32m✓\033[0m %s configured — restart hash to use it.\n", chosen.Name)
		}
	}
}

const onboardingDocsURL = "https://runhash.dev/docs"

// agentAvailable reports whether the effective agent can actually be reached:
// an http transport needs a URL, a stdio transport needs its command on PATH.
// lookPath is injected (exec.LookPath in production) for testability.
func (s *Shell) agentAvailable(lookPath func(string) (string, error)) bool {
	cfg := s.config.EffectiveAgent()
	if cfg.Transport == "http" {
		return cfg.URL != ""
	}
	if cfg.Command == "" {
		return false
	}
	// The Command may embed a subcommand (e.g. "cursor-agent acp" or
	// "gemini --experimental-acp"); the transport splits it via ParsedCommand
	// before spawning, so resolve the same program name here rather than
	// looking up the whole string, which would never be on PATH.
	program, _ := agent.ACPConfig{Command: cfg.Command, Args: cfg.Args}.ParsedCommand()
	_, err := lookPath(program)
	return err == nil
}

// writeNoAgentHint shows friendly guidance when ?? is used but no agent is
// reachable, instead of letting a raw exec failure surface.
func writeNoAgentHint(w io.Writer) {
	fmt.Fprintf(w, "\n\033[31m✗ No AI agent available.\033[0m\n")
	fmt.Fprintf(w, "  Run \033[36msetup\033[0m to configure one, or see %s\n", onboardingDocsURL)
}

// handleInputError is what reading a line ended with means for the loop:
// nil to carry on (Ctrl+C), else the error that ends it. Ctrl+D, or the
// input closing, ends the shell with the last line's status, as in bash.
func (s *Shell) handleInputError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrEditorCanceled):
		// Ctrl+C at empty prompt: just print ^C and continue (like Fish)
		fmt.Fprintln(s.out(), "^C")
		return nil
	case errors.Is(err, ErrEditorEOF):
		fmt.Fprintln(s.out(), "exit")
		return shellExit{status: s.status.exitCode}
	case errors.Is(err, editor.ErrInputBusy) && s.editorCfg.Input != nil:
		// The prompt is the top reader: a claim still held now leaked. Take
		// the keyboard back rather than end the user's login shell.
		was := s.editorCfg.Input.Reset()
		fmt.Fprintf(s.errOut(), "hash: took the keyboard back from %s\n", was)
		trace.ShellHigh("keyboard_leak", map[string]any{"owner": was})
		return nil
	default:
		return err
	}
}

// handleIssueShortcut handles the !! shortcut for quick issue submission:
// `issue --last` for the command before it. It is a line the user ran, so
// its status is the one the prompt and OSC 133 D report, but no command:
// the last command stays the one before it, for a bare ??, a second !! and
// the predictor, and history and the learning loop never see it. It asks
// first, whatever the last command did (issueQuestion); Ctrl+C at the
// question interrupts the line (130), and the question ends when ctx does
// (hash terminated).
func (s *Shell) handleIssueShortcut(ctx context.Context) {
	s.status = lineStatus{}
	fmt.Fprint(s.out(), issueQuestion(s.last))
	switch s.keyboard().yes(ctx, s.out()) {
	case answerInterrupted:
		s.status.exitCode = 130
		return
	case answeredNo:
		return
	}
	if err := s.builtinIssue([]string{"--last"}); err != nil {
		fmt.Fprintf(s.errOut(), "hash: %v\n", err)
		s.status.exitCode = 1
	}
}

// issueQuestion is what !! asks before it opens the editor on an issue
// about the last line, last: always, as opening an editor is not what a
// user who typed !! for bash's history expansion expects.
func issueQuestion(last commandOutcome) string {
	switch {
	case last.command == "":
		return "Open an issue? [y/N] "
	case last.exitCode == 0:
		return "Last command succeeded. Open an issue anyway? [y/N] "
	}
	return fmt.Sprintf("Open an issue about the last command (exit %d)? [y/N] ", last.exitCode)
}

// in, out and errOut are the shell's terminal streams: the process's
// unless the shell was given its own (stdin, stdout, stderr).
func (s *Shell) in() *os.File {
	if s.stdin != nil {
		return s.stdin
	}
	return os.Stdin
}

func (s *Shell) out() *os.File {
	if s.stdout != nil {
		return s.stdout
	}
	return os.Stdout
}

func (s *Shell) errOut() *os.File {
	if s.stderr != nil {
		return s.stderr
	}
	return os.Stderr
}

// dispatchCommand parses and executes a command line. It returns a
// shellExit when the line ended the shell.
func (s *Shell) dispatchCommand(ctx context.Context, line string) error {
	parsed := parser.Parse(line)
	if trace.Enabled("shell") {
		trace.ShellHigh("dispatch", map[string]any{
			"type":    parsed.Type.String(),
			"command": parsed.Command,
			"prompt":  parsed.AgentPrompt,
		})
	}

	switch parsed.Type {
	case parser.CommandTypeEmpty:
		// Nothing ran; the caller draws the next prompt.
	case parser.CommandTypeAgent, parser.CommandTypeAgentPipe, parser.CommandTypeAgentInline:
		if err := s.handleAgentRequest(ctx, line, parsed); err != nil {
			return err
		}
	case parser.CommandTypeRegular:
		_, err := s.runShellLine(ctx, line)
		return err
	}
	return nil
}

// runShellLine runs a shell line the user typed or accepted from the agent,
// builtins included, and sets its outcome. It reports whether the line ran:
// exit ends the shell instead.
func (s *Shell) runShellLine(ctx context.Context, line string) (ran bool, err error) {
	// Check for builtins first
	handled, err := s.executeBuiltin(ctx, line)
	if endsShell(err) {
		return false, err
	}
	if handled || err != nil {
		o := commandOutcome{command: line}
		if err != nil {
			fmt.Fprintf(s.errOut(), "hash: %v\n", err)
			o.exitCode, o.stderr = 1, err.Error()
		}
		s.finishShellLine(o)
		return true, nil
	}

	// Record command to clipboard buffer before execution
	if s.clipboard != nil {
		s.clipboard.AddCommand(line)
	}

	// Capture stderr for issue reporting
	stderrCap := newStderrCapture(s.errOut())

	result, err := s.executeLine(ctx, line, s.out(), stderrCap)
	s.handleExecutionResult(line, result, err, stderrCap)
	if err == nil && result.End == executor.LineExited {
		// The line ran exit in the shell itself (true && exit 4): the shell
		// ends with the status it set.
		return true, shellExit{status: result.ExitCode}
	}
	return true, nil
}

// executeLine runs line in the interpreter with Ctrl+C claimed for it, as
// bash does: a SIGINT the foreground command dies of, or one that comes
// while only in-process work runs (a loop of builtins, read), ends the line
// with status 130, and the shell goes on. The line runs on its own context
// (lineContext), which a background job it starts outlives the line on.
func (s *Shell) executeLine(ctx context.Context, line string, stdout, stderr io.Writer) (*executor.Result, error) {
	return s.runOnLine(newLineContext(ctx), line, stdout, stderr)
}

// runOnLine runs line on the running line lc (executeLine), for a caller
// that reads how lc ended.
func (s *Shell) runOnLine(lc *lineContext, line string, stdout, stderr io.Writer) (*executor.Result, error) {
	return s.onLine(lc, func(ctx context.Context) (*executor.Result, error) {
		return s.executor.Execute(ctx, line, stdout, stderr)
	})
}

// onLine is run, which runs a line's text in the executor on ctx, run as
// the line lc: the one place a line claims Ctrl+C and ends, a line typed
// at the prompt or a startup step (Startup.line).
func (s *Shell) onLine(lc *lineContext, run func(ctx context.Context) (*executor.Result, error)) (*executor.Result, error) {
	release := s.interrupts.claim(func() {
		// A foreground command that runs decides (it ends the line if it
		// dies of the signal); with nothing external running the line
		// ends now.
		if !s.executor.InterruptLine(lc.interrupt) {
			lc.interrupt()
		}
	})
	result, err := run(lc)
	release()
	if err == nil {
		result.ExitCode = lc.status(result)
	}
	lc.returned(lineJobsDone(result))
	return result, err
}

// handleExecutionResult turns an external line's result into its outcome.
func (s *Shell) handleExecutionResult(line string, result *executor.Result, err error, stderrCap *stderrCapture) {
	o := commandOutcome{command: line}
	if err != nil {
		o.exitCode = s.showRefusal(err)
	} else {
		o.exitCode = result.ExitCode
		o.duration = result.Duration
	}
	s.keepOutput(result)

	o.stderr = stderrCap.String()
	if o.stderr == "" {
		// A PTY merges child stderr into its output stream; recover the error
		// text from the captured tail so learning patterns, ?? explanations,
		// and issue reports see what actually went wrong.
		o.stderr = ptyStderrFallback(result)
	}
	if o.stderr == "" {
		// The shell itself refused a command (not found, not executable): its
		// own words are the only error text for learning and a bare ??.
		switch {
		case err != nil:
			o.stderr = err.Error()
		case result != nil && len(result.Refusals) > 0:
			msgs := make([]string, 0, len(result.Refusals))
			for _, r := range result.Refusals {
				msgs = append(msgs, r.Error())
			}
			o.stderr = strings.Join(msgs, "\n")
		}
	}
	if result != nil {
		o.typoFix = s.closeRefusals(line, result.Refusals)
	}
	s.finishShellLine(o)
}

// keepOutput settles the clipboard's entry for the line that gave result
// (nil: the line was refused): what it printed, nothing, or output that
// went straight to the terminal, past hash.
func (s *Shell) keepOutput(result *executor.Result) {
	switch {
	case s.clipboard == nil:
	case result == nil:
		s.clipboard.SetOutput("")
	case result.Uncaptured:
		s.clipboard.SetUncaptured()
	default:
		s.clipboard.SetOutput(result.CapturedOutput)
	}
}

// finishShellLine makes o the line's outcome and feeds it to history, the
// learning loop and the predictor's command sequences.
func (s *Shell) finishShellLine(o commandOutcome) {
	prev := s.last.shellCommand()
	o.cwd, _ = os.Getwd()
	o.id = s.recordCommand(o.command, o.exitCode, o.duration)
	s.last = o
	s.status = o.status()

	// Feed the learning loop: failures may surface a learned fix,
	// a subsequent success records the fix that worked
	s.observeCommandOutcome(o.command)

	// Record command sequence for prediction (only successful commands)
	if s.predictor != nil && o.exitCode == 0 && prev != "" {
		s.predictor.Record(prev, o.command, o.cwd)
	}
}

// finishAgentLine makes a ?? line's own result its outcome, for a turn that
// handed no command to the shell, and records the line (recordAgentLine).
func (s *Shell) finishAgentLine(line string, exitCode int, entered time.Time) {
	s.recordAgentLine(line, exitCode, entered)
	cwd, _ := os.Getwd()
	s.last = commandOutcome{command: line, kind: agentLine, exitCode: exitCode, cwd: cwd}
	s.status = s.last.status()
}

// recordAgentLine records a ?? line in history once its turn is over, with
// the status it ended with, at the time it was entered: it sorts before a
// command its turn handed to the shell, which history has by then.
func (s *Shell) recordAgentLine(line string, exitCode int, entered time.Time) {
	s.recordCommandAt(line, exitCode, 0, entered)
}

// syncStatus sets the interpreter's $? to exitCode once a line is over
// (handleLine): the line may be one it did not run (a ?? request, a builtin
// hash handles itself, a line it refused), and hooks may have run after
// it. The prompt, OSC 133 D and $? then agree on how the line ended.
// Nothing runs for it, so the ERR trap does not fire.
func (s *Shell) syncStatus(exitCode int) {
	if s.executor == nil {
		return
	}
	s.executor.SetLastStatus(exitCode)
}

// showRefusal shows why the shell refused a command and returns the status
// the refusal gives the line. It only draws: it is the executor's refusal
// handler, called mid-line, for hooks and background jobs too, so what the
// refusals mean for the user's line is worked out once the line is over
// (closeRefusals).
func (s *Shell) showRefusal(err error) (exitCode int) {
	handler := s.errorHandler()
	var nex *executor.CommandNotExecutableError
	if errors.As(err, &nex) {
		handler.HandleNotExecutable(nex.Command, nex.Reason)
		return 126
	}

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
		handler.HandleCommandNotFound(cnf.Command, suggestions, installHint)
		return 127
	}
	fmt.Fprintf(s.errOut(), "hash: %v\n", err)
	return 1
}

func (s *Shell) errorHandler() *ErrorHandler {
	if s.errors == nil {
		return NewErrorHandler()
	}
	return s.errors
}

// closeRefusals ends the banners the user's line drew for mistyped command
// names with one footer, and returns the line with every one of them
// corrected, for the next prompt to offer. There is no corrected line when
// the learning loop is off, or a refusal has no correction in the line.
func (s *Shell) closeRefusals(line string, refusals []error) (typoFix string) {
	fixed, notFound := line, false
	corrected := map[string]bool{}
	for _, r := range refusals {
		var cnf *executor.CommandNotFoundError
		if !errors.As(r, &cnf) {
			fixed = "" // not executable: no name to correct
			continue
		}
		notFound = true
		if fixed != "" && !corrected[cnf.Command] { // a loop refuses one name many times
			corrected[cnf.Command] = true
			fixed = correctTypo(fixed, cnf.Command, s.suggestor.Suggest(cnf.Command))
		}
	}
	if !notFound {
		return ""
	}
	if !s.fixes.active() {
		fixed = ""
	}
	s.errorHandler().RefusalFooter(fixed != "")
	return fixed
}

// correctTypo replaces typo with the first suggestion wherever it stands as
// a word in line, or returns "" when there is no suggestion or the name is
// not in the line (it came from a function or a variable).
func correctTypo(line, typo string, suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	return typoFixLine(line, typo, suggestions[0])
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
			fmt.Fprintf(s.errOut(), "hash: init_commands does not support agent syntax: %s\n", line)
			continue

		case parser.CommandTypeRegular:
			handled, err := s.executeBuiltin(ctx, line)
			if endsShell(err) {
				return err
			}
			if err != nil {
				fmt.Fprintf(s.errOut(), "hash: init command failed: %s: %v\n", line, err)
				continue
			}
			if handled {
				continue
			}

			// A line of the shell, as one typed at the prompt: Ctrl+C
			// interrupts it and the next one runs.
			result, err := s.executeLine(ctx, line, s.out(), s.errOut())
			switch {
			case err != nil:
				fmt.Fprintf(s.errOut(), "hash: init command failed: %s: %v\n", line, err)
			case result.End == executor.LineExited:
				return shellExit{status: result.ExitCode}
			}
		}
	}

	return nil
}

// ErrEditorCanceled is returned when the editor is canceled (Ctrl+C).
var ErrEditorCanceled = errors.New("editor canceled")

// ErrEditorEOF is returned when the editor receives EOF (Ctrl+D).
var ErrEditorEOF = errors.New("editor EOF")

// readLineWithEditor reads the next line the user enters. Reading moves
// between two states: an editor edits the line, and a picker the editor
// exited for (Ctrl+R, Ctrl+P) runs with the terminal handed to it and
// gives the next editor the line to start from. The prompt resumes where
// the picker opened: the editor clears its input row, the picker draws
// there (inline, or on the alternate screen) and leaves the cursor on that
// row, and the next editor draws on it, under the info bar still on
// screen. It ends with the line an
// editor submits, or an error: Ctrl+C, Ctrl+D, the input failing, or the
// shell ending (ctx), after which no editor comes up again.
func (s *Shell) readLineWithEditor(ctx context.Context) (string, error) {
	// Reset history navigation state for new input session
	s.historyNav.reset()
	if s.executor != nil {
		// Before the keys a reader held: no line runs on a PTY once one
		// holds keys (TestKeyboard_TypedDuringComesBeforeTheKeysAReaderHeld).
		s.keyboard().typedDuring(s.executor.TakeUnread())
	}

	text := ""
	for {
		result, err := s.editLine(ctx, text)
		if err != nil {
			return "", err
		}
		switch result.Exit {
		case editor.ExitEOF:
			return "", ErrEditorEOF
		case editor.ExitCanceled:
			return "", ErrEditorCanceled
		case editor.ExitHistorySearch, editor.ExitContextPicker:
			if text, err = s.runPicker(ctx, result); err != nil {
				return "", err
			}
		default:
			return result.Text, nil
		}
	}
}

// editLine runs one editor on the prompt, starting with text.
func (s *Shell) editLine(ctx context.Context, text string) (editor.Result, error) {
	cfg := s.editorCfg
	cfg.Prompt = s.currentPromptLine()

	ed := editor.New(cfg, s.in(), s.out())
	if text != "" {
		ed.SetInitialText(text)
	}

	// Ghost text: learned fix for the last failure, else prediction
	if ghost := s.promptGhost(); ghost != "" {
		ed.SetGhostText(ghost)
	}
	return ed.Run(ctx)
}

// currentPromptLine returns the current prompt line (without prefix).
func (s *Shell) currentPromptLine() string {
	cwd, _ := os.Getwd()
	ctx := prompt.PromptContext{
		Cwd:        cwd,
		ExitCode:   s.status.exitCode,
		DurationMs: s.status.duration.Milliseconds(),
	}
	_, promptLine := s.prompt.GenerateMultiLine(ctx)
	return promptLine
}

// runPicker runs the picker an editor exited for (Ctrl+R, Ctrl+P), with
// the terminal handed off to it, and returns the line the next editor
// starts with: the history pick, else the editor's line. It returns ctx's
// error when the shell ended while the picker ran.
func (s *Shell) runPicker(ctx context.Context, result editor.Result) (string, error) {
	h, _ := s.keyboard().handOff()
	defer h.release()
	text := result.Text
	if result.Exit == editor.ExitHistorySearch {
		if selected := s.runHistoryPicker(ctx); selected != "" {
			text = selected
		}
	} else {
		s.runContextPicker(ctx)
	}
	return text, ctx.Err()
}

// runHistoryPicker launches the Ctrl+R history picker and returns the selected command.
func (s *Shell) runHistoryPicker(ctx context.Context) string {
	if s.history == nil {
		return ""
	}
	picker := history.NewSearchUI(s.history, s.colorPalette)
	picker.SetClipboard(s.clipboard)
	selected, err := picker.Run(ctx)
	if err != nil {
		return ""
	}
	return selected
}

// runContextPicker launches the context picker TUI on the current picks
// and remembers what the user picked.
func (s *Shell) runContextPicker(ctx context.Context) {
	picks, err := hashcontext.NewPickerUI(s.pickedContext()).Run(ctx)
	if err != nil {
		// Picker canceled, errored or ended with the shell: keep the
		// existing selection
		return
	}
	s.contextPicks = picks
}

// contextSelection resolves the Ctrl+P picks against the context as it is
// now, or nil for the auto-detected defaults. A pick that no longer exists
// (a command gone from recent history) drops out.
func (s *Shell) contextSelection() *hashcontext.Collection {
	if s.contextPicks.Defaults() {
		return nil
	}
	return s.pickedContext()
}

// pickedContext is the context on offer now, marked with the Ctrl+P picks
// (or its defaults when there are none yet).
func (s *Shell) pickedContext() *hashcontext.Collection {
	collection := s.contextCollection()
	s.contextPicks.Apply(collection)
	return collection
}

// contextCollection is the context on offer right now: auto-detected
// items, recent history, the last output and common env vars.
func (s *Shell) contextCollection() *hashcontext.Collection {
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

	return builder.Build()
}

// agentRequestLabel names the ?? form that is about to run. A plain request
// is an agent turn whose reply may be output or a command, so only the pipe
// and inline forms carry a qualifier.
func agentRequestLabel(typ parser.CommandType) string {
	switch typ {
	case parser.CommandTypeAgentPipe:
		return "[agent: pipe]"
	case parser.CommandTypeAgentInline:
		return "[agent: inline]"
	default:
		return "[agent]"
	}
}

func (s *Shell) handleAgentRequest(ctx context.Context, line string, parsed parser.ParseResult) error {
	entered := time.Now()
	if s.agentHandler == nil {
		drawAgentLabel(s.out(), parsed.Type)
		writeAgentNotConfiguredHint(s.errOut())
		s.finishAgentLine(line, 1, entered)
		return nil
	}

	// Pre-flight: if the configured agent can't be reached (adapter missing
	// from PATH, or http transport with no URL), show friendly guidance
	// instead of letting a raw exec failure surface.
	if !s.agentAvailable(exec.LookPath) {
		drawAgentLabel(s.out(), parsed.Type)
		writeNoAgentHint(s.errOut())
		s.finishAgentLine(line, 1, entered)
		return nil
	}
	if parsed.Type != parser.CommandTypeAgentPipe {
		drawAgentLabel(s.out(), parsed.Type) // a pipe's once its command ran (runAgentTurn)
	}

	// The turn owns Ctrl+C and Esc while it runs: they cancel the agent
	// request without exiting the shell. Whatever path it takes, the turn
	// leaves the terminal no spinner, progress bar or stale status.
	t := s.beginTurn(ctx)
	defer t.end()

	// Every request of the turn carries the Ctrl+P picks and, so a bare ??
	// can explain it, the line that failed.
	t.facts = requestFacts{selection: s.contextSelection(), lastError: s.last.failedCommand()}

	// Use unified streaming handler for all modes. A command the turn hands
	// to the shell sets the line's outcome itself.
	err := s.runAgentTurn(t, parsed)
	if exitCode, ok := t.lineExitCode(); ok {
		s.finishAgentLine(line, exitCode, entered)
	} else {
		s.recordAgentLine(line, 0, entered) // the agent's work finished; the command it handed off is the line's outcome
	}
	return err
}

// drawAgentLabel draws the label a ?? request's row starts with, the
// spinner and the reply following it on the row.
func drawAgentLabel(w io.Writer, typ parser.CommandType) {
	fmt.Fprintf(w, "\033[90m%s\033[0m ", agentRequestLabel(typ))
}

func (s *Shell) agentRequestTimeout() time.Duration {
	agentCfg := s.config.EffectiveAgent()
	timeout := 120 * time.Second // Match config default
	if agentCfg.Timeout != "" {
		if parsedDur, err := time.ParseDuration(agentCfg.Timeout); err == nil {
			timeout = parsedDur
		}
	}
	return timeout
}

// handleAgentInlineStreaming uses ghost text for inline completions. The
// turn follows the fill's stream, not the editor: when the fill has arrived
// (or failed) the agent's work is over while the user may still be editing.
func (s *Shell) handleAgentInlineStreaming(t *liveTurn, parsed parser.ParseResult) error {
	requestCtx, timeoutCancel := context.WithTimeout(t.requestInline(parsed.AgentPrompt), s.agentRequestTimeout())
	defer timeoutCancel()

	// Build initial text for editor
	initialText := parsed.Command

	// Start a frame-paced event stream. The adapter keeps lifecycle events out
	// of the editable ghost completion while preventing per-character redraws.
	streamDone := make(chan struct{})
	var proposed string // the line as the ghost offered it; read once streamDone closes
	events, errCh := s.agentHandler.StreamEvents(requestCtx, parsed, t.facts)
	events, errCh = paceAgentEvents(requestCtx, events, errCh, agentStreamPacerOptions{})
	updates, errCh := textStreamFromEvents(requestCtx, events, errCh, func(fill string, err error) {
		proposed = initialText + fill
		t.inlineEnded(proposed, err)
		close(streamDone)
	})

	// Reset history navigation
	s.historyNav.reset()

	ed := editor.New(s.agentTextEditorConfig(), s.in(), s.out())
	if initialText != "" {
		ed.SetInitialText(initialText)
	}

	// Set up streaming ghost text with model name
	ed.SetGhostTextStreaming(updates, errCh, timeoutCancel)

	// Run the editor on the turn's context, not the request's: the timeout
	// bounds how long we wait for the agent, never how long the user may edit.
	// The user leaves the line one of two ways: Enter on a line runs it;
	// anything else (Esc, Ctrl+C, Ctrl+D, Enter on an empty line, an editor
	// error) runs nothing, ends the turn (inlineLeft), and the line's status
	// is how the request ended (lineExitCode).
	result, err := ed.Run(t.ctx)
	accepted := strings.TrimSpace(result.Text)
	runs := err == nil && result.Exit == editor.ExitSubmit && accepted != ""
	if !runs {
		t.inlineLeft()
	}
	// The request is over whatever the user did: wait for its stream, so
	// the turn knows how it ended before the line's status is read.
	timeoutCancel()
	<-streamDone

	if err != nil {
		t.editorFailed(err)
	}
	if !runs {
		return nil
	}

	// The user may have edited the line into anything, including a builtin
	// such as exit or cd, so it takes the same path as a line typed at the
	// prompt: builtins, history, learning and stderr capture included. As
	// with an agent command the user edits, a ?? the fill brought is shell
	// syntax and one the user typed is a request. The turn is over before
	// it runs, the stream with it.
	_, err = s.runEditedCommand(t.handOff(), proposed, accepted)
	return err
}

// pipeOutcome is how the command of `cmd | ?? prompt` ended, which decides
// what the turn does next and, when no request goes, the ?? line's status.
type pipeOutcome int

const (
	pipeRan         pipeOutcome = iota // it ran to its end, whatever its status: what it printed goes with the request
	pipeInterrupted                    // Ctrl+C ended it, as a line: no request goes, and the line ends 130
	pipeShellEnded                     // the shell's ending cut it off: the turn ends with the shell (ending)
	pipeFailed                         // hash could not run it (a refusal): the turn fails
)

// pipeCommand is the command of `cmd | ?? prompt` once it ended.
type pipeCommand struct {
	outcome pipeOutcome
	output  string // what it printed (pipeRan)
	err     error  // why it could not run (pipeFailed)
}

// runPipeCommand runs the command of `cmd | ?? prompt` as a line of the
// shell (runOnLine, on shell, the shell's context), as one typed at the
// prompt: Ctrl+C interrupts it, and a job it starts outlives the turn. It
// runs in a subshell, as the left side of a pipe does in bash: a cd or an
// assignment stays in it, and exit or errexit end only it. How it ended is
// decided here, once, from its line: the shell's ending first, which makes
// whatever the interpreter saw a cut-off.
func (s *Shell) runPipeCommand(shell context.Context, command string) pipeCommand {
	if err := s.checkSyntax(command); err != nil {
		return pipeCommand{outcome: pipeFailed, err: err}
	}
	var out strings.Builder
	lc := newLineContext(shell)
	result, err := s.runOnLine(lc, "( "+command+"\n)", &out, s.errOut())
	switch {
	case err != nil:
		return pipeCommand{outcome: pipeFailed, err: err}
	case lc.shellEnded():
		return pipeCommand{outcome: pipeShellEnded}
	case result.End == executor.LineInterrupted:
		return pipeCommand{outcome: pipeInterrupted}
	}
	return pipeCommand{outcome: pipeRan, output: out.String()}
}

// checkSyntax parses command, as a line of its own, in the shell's dialect:
// a parse error names the lines and columns the user typed, where one of a
// line hash wraps (a pipe command's subshell) named the wrapper's.
func (s *Shell) checkSyntax(command string) error {
	var lang syntax.LangVariant
	if err := lang.Set(s.executor.Dialect()); err != nil {
		lang = syntax.LangBash
	}
	_, err := shparse.Parse(syntax.NewParser(syntax.Variant(lang)), command+"\n", "hash")
	return err
}

// runAgentTurn runs the requests of the turn t for the ?? line parsed, in
// whichever form it has: full, pipe or inline.
func (s *Shell) runAgentTurn(t *liveTurn, parsed parser.ParseResult) error {
	// For pipe mode, capture command output first
	if parsed.Type == parser.CommandTypeAgentPipe {
		pc := s.runPipeCommand(t.parent, parsed.Command)
		switch pc.outcome {
		case pipeShellEnded:
			t.ending() // now, before any other event of the turn: it draws nothing
			return nil
		case pipeInterrupted:
			t.cutOff()
			return nil
		case pipeFailed:
			drawAgentLabel(s.out(), parsed.Type)
			t.failed(fmt.Sprintf("pipe command failed: %v", pc.err))
			return nil
		}
		drawAgentLabel(s.out(), parsed.Type)
		// Update context with pipe output
		if s.clipboard != nil {
			s.clipboard.AddCommand(parsed.Command)
			s.clipboard.SetOutput(pc.output)
		}
	}

	// For inline mode, use editor with ghost text
	if parsed.Type == parser.CommandTypeAgentInline {
		return s.handleAgentInlineStreaming(t, parsed)
	}

	// Full ?? and pipe modes: streaming with confirmation UI
	return s.handleAgentFullStreaming(t, parsed)
}

// handleAgentFullStreaming handles full ?? and pipe modes with streaming.
func (s *Shell) handleAgentFullStreaming(t *liveTurn, parsed parser.ParseResult) error {
	responseText, latency, ok := s.streamFirstReply(t, parsed)
	if !ok {
		return nil
	}
	turn := agentTurn{prompt: parsed.AgentPrompt, response: responseText, latency: latency}

	resp, next, confirmType := classifyReply(parsed.Type, false, responseText, t.demoteDenied)
	transcript := s.initialAgentConversationTranscript(parsed, t.facts, responseText)
	t.answered(resp, responseText, next, confirmType)
	if next != replyAwaitsKey {
		s.recordAgentTurn(turn, true, 0)
		return nil
	}

	action := t.confirm(confirmType)
	if action == ConfirmReply {
		s.clearConfirmationHint() // the reply box takes the hint's place
		s.recordAgentTurn(turn, true, 0)
		return s.runAgentConversationLoop(t, transcript)
	}

	return s.handleAgentConfirmAction(t, action, confirmType, resp, turn)
}

// streamFirstReply sends the turn's request and draws the reply, retrying
// a failure while the user asks to. It reports the reply and how long its
// request took, or false when there is none to act on (the turn then ended
// canceled or failed).
func (s *Shell) streamFirstReply(t *liveTurn, parsed parser.ParseResult) (responseText string, latency time.Duration, ok bool) {
	for {
		started := time.Now()
		streamResult := s.streamTurnRequest(t, parsed.AgentPrompt, func(ctx context.Context) (<-chan agent.StreamEvent, <-chan error) {
			return s.agentHandler.StreamEvents(ctx, parsed, t.facts)
		}, "", replyWidth(0), s.agentOutput.WriteStream)

		if streamResult.canceled {
			t.canceled("")
			return "", 0, false
		}
		if streamResult.streamErr != nil {
			if s.handleAgentStreamError(t, streamResult.streamErr, len(streamResult.responseText)) {
				continue
			}
			return "", 0, false
		}
		responseText = strings.TrimSpace(streamResult.responseText)
		if responseText == "" {
			t.failed(emptyAgentResponseMessage)
			return "", 0, false
		}
		return responseText, time.Since(started), true
	}
}

// streamTurnRequest sends one request of the turn and draws its reply as it
// streams: start opens the request's events on the request's context, write
// draws rendered text, and tool rows are drawn behind toolPrefix.
func (s *Shell) streamTurnRequest(
	t *liveTurn,
	request string,
	start func(context.Context) (<-chan agent.StreamEvent, <-chan error),
	toolPrefix string,
	wrapWidth int,
	write func(string),
) agentStreamCollectionResult {
	t.request(request)
	requestCtx, timeoutCancel := context.WithTimeout(t.ctx, s.agentRequestTimeout())
	defer timeoutCancel()

	events, errCh := start(requestCtx)
	events, errCh = paceAgentEvents(requestCtx, events, errCh, agentStreamPacerOptions{})
	result := s.collectAgentEventStream(t.ctx, events, errCh, agentStreamCollectionOptions{
		beforeText:    t.text,
		writeRendered: write,
		wrapWidth:     wrapWidth,
	}, func(update agent.ToolCallUpdate) {
		t.tool(update, toolPrefix)
	})
	t.streamEnded()
	return result
}

func agentToolActivityLabel(update agent.ToolCallUpdate) string {
	title := sanitizeTerminalText(update.Title)
	if title == "" {
		title = "tool call"
	}
	return "agent · running · " + truncateTerminalText(title, 72)
}

func (s *Shell) initialAgentConversationTranscript(parsed parser.ParseResult, facts requestFacts, responseText string) []agentConversationMessage {
	userPrompt := strings.TrimSpace(parsed.AgentPrompt)
	if s.agentHandler != nil {
		if req, err := s.agentHandler.buildRequest(parsed, facts); err == nil {
			userPrompt = strings.TrimSpace(req.Prompt)
		}
	}

	transcript := make([]agentConversationMessage, 0, 2)
	if userPrompt != "" {
		transcript = append(transcript, agentConversationMessage{Role: "user", Text: userPrompt})
	}
	if responseText != "" {
		transcript = append(transcript, agentConversationMessage{Role: "assistant", Text: responseText})
	}
	return transcript
}

func (s *Shell) runAgentConversationLoop(t *liveTurn, transcript []agentConversationMessage) error {
	openRail := true
	for {
		t.replying(lastAssistantText(transcript))
		reply, ok := s.readAgentConversationReply(t.ctx, openRail)
		if !ok {
			return nil
		}
		openRail = false
		if agentConversationReplyEndsConversation(reply) {
			return nil
		}

		priorTranscript := append([]agentConversationMessage(nil), transcript...)
		started := time.Now()
		responseText, ok := s.streamAgentFollowUpTurn(t, reply, priorTranscript)
		transcript = append(transcript, agentConversationMessage{Role: "user", Text: reply})
		if !ok {
			return nil
		}
		transcript = append(transcript, agentConversationMessage{Role: "assistant", Text: responseText})
		turn := agentTurn{prompt: reply, response: responseText, latency: time.Since(started)}

		resp, next, confirmType := classifyReply(parser.CommandTypeAgent, true, responseText, t.demoteDenied)
		t.answered(resp, responseText, next, confirmType)
		switch next {
		case replyAsksBack:
			s.recordAgentTurn(turn, true, 0)
			if s.keyboard().typedAhead() {
				return nil // keys typed during the follow-up are the next prompt's
			}
			continue
		case replyStands:
			s.recordAgentTurn(turn, true, 0)
			return nil
		}

		action := t.confirm(confirmType)
		if action == ConfirmReply {
			s.clearConfirmationHint()
			s.recordAgentTurn(turn, true, 0)
			continue
		}
		return s.handleAgentConfirmAction(t, action, confirmType, resp, turn)
	}
}

func (s *Shell) readAgentConversationReply(ctx context.Context, openRail bool) (string, bool) {
	cfg := agentConversationEditorConfig(s.editorCfg, openRail)
	s.historyNav.reset() // Up recalls from the newest entry, not where the prompt left off

	ed := editor.New(cfg, s.in(), s.out())
	result, err := ed.Run(ctx)
	if err != nil || result.Exit == editor.ExitCanceled || result.Exit == editor.ExitEOF {
		return "", false
	}

	reply := strings.TrimSpace(result.Text)
	return reply, reply != ""
}

// streamAgentFollowUpTurn sends a conversation reply and draws the agent's
// answer behind the conversation rail. A failure is offered a retry exactly
// as the first request's is. It reports the answer, or false when there is
// none to act on.
func (s *Shell) streamAgentFollowUpTurn(
	t *liveTurn,
	reply string,
	transcript []agentConversationMessage,
) (responseText string, ok bool) {
	for {
		railPrefixer := newAgentConversationRailPrefixer(s.agentOutput.WriteStream)
		streamResult := s.streamTurnRequest(t, reply, func(ctx context.Context) (<-chan agent.StreamEvent, <-chan error) {
			return s.agentHandler.StreamFollowUpEvents(ctx, reply, transcript, t.facts)
		}, agentConversationAgentContinuation, replyWidth(agentConversationAgentPrefixWidth), railPrefixer.Write)

		if streamResult.canceled {
			t.canceled(agentConversationAgentContinuation)
			return "", false
		}

		if streamResult.streamErr != nil {
			if s.handleAgentStreamError(t, streamResult.streamErr, len(streamResult.responseText)) {
				continue
			}
			return "", false
		}

		responseText = strings.TrimSpace(streamResult.responseText)
		if responseText == "" {
			t.failed(emptyAgentResponseMessage)
			return "", false
		}
		return responseText, true
	}
}

// replyNext is what a complete reply asks of the user.
type replyNext int

const (
	replyStands    replyNext = iota // nothing: the answer stands (a pipe turn's prose)
	replyAwaitsKey                  // a confirmation hint waits for a key; its type says which
	replyAsksBack                   // in a conversation, a question: the reply box opens at once
)

// classifyReply decides what a complete reply asks of the user, the same way
// for every turn of the ?? form typ. The one deliberate difference is the
// conversation: before it, a reply that asks a question still shows the hint
// (the user opts in with r); inside it, the question opens the reply box
// directly. demote turns a command the user denied into prose.
func classifyReply(typ parser.CommandType, inConversation bool, responseText string, demote func(agent.Response) agent.Response) (agent.Response, replyNext, ConfirmationType) {
	collector := agent.NewStreamCollector()
	collector.Append(responseText)
	resp := collector.Response()

	full := typ == parser.CommandTypeAgent
	wantsReply := full && agentResponseWantsReply(responseText)
	// A natural-language question misread as a command is prose.
	if wantsReply && resp.Type == agent.ResponseTypeCommand && agentConversationLooksLikeQuestion(responseText) {
		resp = agent.Response{Type: agent.ResponseTypeExplanation, Explanation: responseText}
	}
	if inConversation && wantsReply && resp.Type == agent.ResponseTypeExplanation {
		return resp, replyAsksBack, 0
	}
	if demote != nil {
		resp = demote(resp)
	}
	switch {
	case resp.Type == agent.ResponseTypeCommand:
		return resp, replyAwaitsKey, ConfirmTypeCommand
	case resp.Type == agent.ResponseTypeExplanation && full:
		return resp, replyAwaitsKey, ConfirmTypeExplanation
	default:
		// Pipe prose, or an error reply: the text says it all, and there is
		// nothing for a key to do.
		return resp, replyStands, 0
	}
}

// agentTurn is what the shell remembers about one ?? exchange once the user
// has acted on the reply.
type agentTurn struct {
	prompt   string        // what the user asked, as typed after ??
	response string        // the agent's reply text
	latency  time.Duration // request to end of reply
}

// recordAgentTurn stores a ?? exchange in history, linked to the command it
// produced when one ran, so the question and its outcome can be recalled
// together.
func (s *Shell) recordAgentTurn(turn agentTurn, accepted bool, commandID int64) {
	if s.history == nil {
		return
	}
	agentName := ""
	if s.agentHandler != nil {
		agentName = s.agentHandler.CurrentModel()
	}
	if agentName == "" && s.config != nil {
		agentName = s.config.EffectiveAgent().Command
	}
	_, _ = s.history.AddAgentInteraction(history.AgentInteraction{
		Prompt:    turn.prompt,
		Response:  turn.response,
		Accepted:  accepted,
		CommandID: commandID,
		LatencyMs: turn.latency.Milliseconds(),
		Agent:     agentName,
		Timestamp: time.Now(),
	})
}

// handleAgentConfirmAction processes the user's confirmation choice. A
// command the user runs or edits takes the same path as a typed line, so
// the error it returns (such as exit) is the shell's to act on. The turn is
// over first: the command runs on the shell's context, not the turn's.
func (s *Shell) handleAgentConfirmAction(t *liveTurn, action ConfirmAction, confirmType ConfirmationType, resp agent.Response, turn agentTurn) error {
	if action == ConfirmEnded {
		return nil // the shell is ending: the reply stays as it is, unrecorded
	}
	var ran bool
	var err error
	switch action {
	case ConfirmRun:
		if confirmType == ConfirmTypeCommand {
			ctx := t.handOff()
			s.showAgentCommandAsTyped(resp.Command)
			ran, err = s.runAgentCommand(ctx, resp.Command)
			break
		}
		// An explanation is done: drop its hint, keep the answer.
		t.acted()
		s.clearConfirmationHint()
	case ConfirmEdit:
		if confirmType == ConfirmTypeCommand {
			s.clearConfirmationHint() // the editor takes the hint's place
			ran, err = s.handleEditCommand(t, resp.Command)
			break
		}
		// Copy explanation to system clipboard
		t.acted()
		s.clearConfirmationHint()
		if copyErr := copyToSystemClipboard(turn.response); copyErr != nil {
			fmt.Fprintf(s.errOut(), "\033[90mCould not copy: %v\033[0m\n", copyErr)
		} else {
			fmt.Fprintf(s.out(), "\033[90mCopied to clipboard\033[0m\n")
		}
	default:
		// Declining keeps what the agent said, a proposed command included:
		// the user has read it, its lead-in explains it, and rows already
		// scrolled away could not be erased anyway. Only the hint goes.
		t.acted()
		s.clearConfirmationHint()
	}
	accepted := action != ConfirmCancel
	var commandID int64
	if confirmType == ConfirmTypeCommand {
		// A command reply is accepted once a command ran (or ended the
		// shell), whether or not history could record it; an edit the user
		// abandons is not acceptance.
		accepted = ran || endsShell(err)
		if ran {
			commandID = s.last.id
		}
	}
	s.recordAgentTurn(turn, accepted, commandID)
	return err
}

// clearConfirmationHint removes the hint line and the blank line printed
// after the user's keypress, so what follows takes their place.
func (s *Shell) clearConfirmationHint() {
	s.responseUI.ClearLines(2)
}

// showAgentCommandAsTyped replaces the confirmation hint with the command on
// its own input line, so scrollback reads as if the user had typed it.
func (s *Shell) showAgentCommandAsTyped(command string) {
	s.clearConfirmationHint()
	cfg := s.editorCfg
	cfg.Prompt = s.currentPromptLine()
	editor.New(cfg, strings.NewReader(""), s.responseUI.out).PrintSubmitted(command)
}

// runAgentCommand runs a command the agent handed back exactly as if the
// user had typed it: builtins, history, learning, prediction and stderr
// capture included, so a failure becomes the context a bare ?? explains.
// The reply is a shell line, never a new request: a ?? inside it (a glob, a
// grep pattern) is shell syntax, so it bypasses the ?? dispatch.
// It reports whether a command ran; its outcome is then s.last.
func (s *Shell) runAgentCommand(ctx context.Context, command string) (ran bool, err error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, nil
	}
	return s.runShellLine(ctx, command)
}

// handleAgentStreamError shows a failed request and, when trying again can
// help (a transient failure, or one partway through the reply), offers a
// retry. It reports whether the user asked for one; the hint is gone either
// way, and the error stays.
func (s *Shell) handleAgentStreamError(t *liveTurn, streamErr error, responseLen int) (retry bool) {
	t.failed(agentStreamErrorMessage(streamErr))

	// If no response was received, classify the failure before showing hints.
	// Startup errors get install/PATH hints; transient errors get retry.
	if responseLen == 0 {
		switch {
		case agent.IsModelSelectionError(streamErr):
			s.responseUI.ShowModelHint()
			return false
		case agent.IsStartupError(streamErr):
			s.responseUI.ShowAgentHint(
				s.config.Agent.Transport,
				s.config.Agent.Command,
				s.config.Agent.URL,
			)
			return false
		case !agent.IsRetryableError(streamErr):
			return false
		}
	}

	action := t.confirm(ConfirmTypeError)
	if action != ConfirmEnded {
		s.clearConfirmationHint()
	}
	return action == ConfirmRun
}

func writeAgentNotConfiguredHint(w io.Writer) {
	fmt.Fprintf(w, "\n\033[31m✗ Agent not configured.\033[0m\n")
	fmt.Fprintf(w, "  Install the default ACP adapter:\n")
	fmt.Fprintf(w, "  %s\n", claudeAgentACPInstallCommand)
	fmt.Fprintf(w, "  Configure an agent in ~/.config/hash/config.toml:\n")
	fmt.Fprintf(w, "  [agent]\n")
	fmt.Fprintf(w, "  command = \"claude-agent-acp\"\n")
	fmt.Fprintf(w, "  See docs/config-reference.md for options.\n")
}

// handleEditCommand opens the editor on the command the turn produced and
// runs the edited line. It reports whether a command ran. The turn hands off
// only once the user submits a line: an edit left without one (Esc,
// Ctrl+C, Ctrl+D, an empty line) declines the reply, and an editor error fails the
// turn, as in the inline form.
func (s *Shell) handleEditCommand(t *liveTurn, command string) (ran bool, err error) {
	ctx := t.editing()
	s.historyNav.reset()

	ed := editor.New(s.agentTextEditorConfig(), s.in(), s.out())
	ed.SetInitialText(command)

	result, err := ed.Run(ctx)
	switch {
	case err != nil:
		t.editorFailed(err)
		return false, nil
	case result.Exit != editor.ExitSubmit || strings.TrimSpace(result.Text) == "":
		t.acted()
		return false, nil
	}
	return s.runEditedCommand(t.handOff(), command, result.Text)
}

// agentTextEditorConfig is the editor the user edits an agent's command or
// inline fill in. It has no history search or context picker: those end the
// editor so the prompt can open them, and here the only way out that runs
// the line is Enter. Esc or one Ctrl+C declines what the agent offered,
// whatever the line holds, as Esc declines a hint: the editor ends, and
// the turn with it, so the prompt comes back. Left open on an emptied
// line, it looked like the prompt but was still the turn's.
func (s *Shell) agentTextEditorConfig() editor.Config {
	cfg := s.editorCfg
	cfg.Prompt = s.currentPromptLine()
	cfg.DisableHistorySearch = true
	cfg.DisableContextPicker = true
	cfg.Decline = editor.DeclineKeepRow
	return cfg
}

// runEditedCommand runs the line the user made of what the agent proposed
// (its command, or the partial line with its inline fill). The edits are
// the user's typing, so a ?? they added is a request, as at the prompt; a
// ?? the agent's proposal already had stays shell syntax. It reports
// whether a shell command ran (a request is not one).
func (s *Shell) runEditedCommand(ctx context.Context, original, edited string) (ran bool, err error) {
	line := strings.TrimSpace(edited)
	if !strings.Contains(original, "??") && parser.Parse(line).Type != parser.CommandTypeRegular {
		return false, s.dispatchCommand(ctx, line)
	}
	return s.runAgentCommand(ctx, edited)
}

// drawPromptHead draws what comes before a prompt's input row, once the
// prompt started (its OSC 133 A, after the last line's D, so a terminal
// that keeps each command's output as a block does not add it to the last
// command's): the notices hash has for the user, then the prompt's info
// bar. generated is the first prompt's, made with the palette at startup;
// nil makes it. It is the one place both are drawn, so nothing lands
// between the bar and the input row the editor draws under it.
func (s *Shell) drawPromptHead(generated *string) {
	s.printNotices()
	if s.prompt == nil {
		return
	}
	if generated != nil {
		printPromptInfoBar(s.out(), *generated)
		return
	}
	s.printPromptPrefix()
}

// printNotices prints what hash tells the user before a prompt: a newer
// adapter (flushUpdateNotice), and the jobs that ended, as bash says
// before its prompt.
func (s *Shell) printNotices() {
	s.flushUpdateNotice(time.Now())
	if s.executor == nil {
		return
	}
	for _, notice := range s.executor.JobNotices() {
		fmt.Fprintln(s.errOut(), notice)
	}
}

// printPromptPrefix prints the multi-line prompt's prefix (the info bar above
// the input line), once for each prompt but the first (drawPromptHead);
// the input line itself is drawn by the editor from currentPromptLine.
func (s *Shell) printPromptPrefix() {
	cwd, _ := os.Getwd()
	ctx := prompt.PromptContext{
		Cwd:        cwd,
		ExitCode:   s.status.exitCode,
		DurationMs: s.status.duration.Milliseconds(),
	}
	printPromptInfoBar(s.out(), s.prompt.Generate(ctx))
}

// initPromptAndPalette generates the first prompt, which it returns, and
// extracts the color palette concurrently, minimizing starship subprocess
// calls (5 to 2 starship calls in all).
func (s *Shell) initPromptAndPalette() string {
	cwd, _ := os.Getwd()
	ctx := prompt.PromptContext{
		Cwd:        cwd,
		ExitCode:   s.status.exitCode,
		DurationMs: s.status.duration.Milliseconds(),
	}

	// Run display prompt and error palette extraction concurrently
	var full string
	var errorOutput string
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		full = s.prompt.Generate(ctx)
	}()
	go func() {
		defer wg.Done()
		errorOutput = s.prompt.GenerateForPalette(1)
	}()
	wg.Wait()

	// Reuse the display output for success palette (exit code 0 at startup)
	successOutput := full
	if s.status.exitCode != 0 {
		successOutput = s.prompt.GenerateForPalette(0)
	}

	// Extract palette from the outputs we already have
	newPalette := prompt.ExtractPaletteFromOutputs(successOutput, errorOutput)
	if newPalette.Primary != prompt.DefaultPalette().Primary {
		s.colorPalette = newPalette
		s.editorCfg.ScrollbarColor = newPalette.Primary
	}
	return full
}

// printPromptInfoBar prints the rows of a multi-line prompt above its input
// line; the editor draws the input line.
func printPromptInfoBar(w io.Writer, full string) {
	if lastNL := strings.LastIndex(full, "\n"); lastNL >= 0 {
		fmt.Fprint(w, full[:lastNL+1])
	}
}

// environValue is name's value in a NAME=value list, "" when unset.
func environValue(environ []string, name string) string {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
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

func makeEditorPrefetchFunc(router *completion.Router) func(string, int) {
	return func(line string, pos int) {
		router.PrefetchBounded(line, pos)
	}
}

func makeEditorSuggestionFunc(store *history.Store, pred *prediction.Predictor) func(string) string {
	return func(input string) string {
		// Try history prefix search first
		if store != nil {
			matches, err := store.SearchByPrefix(input, 1)
			if err == nil && len(matches) > 0 {
				return matches[0]
			}
		}

		_ = pred // predictor fallback reserved for future use

		return ""
	}
}

// CloseOnSignal is Close for a shell the fatal signal sig ended: every job
// still running gets SIGHUP, whatever sig is, as bash resends only SIGHUP
// to its jobs (a nohup job runs on, a SIGTERM to hash too), and 128+sig is
// $? for the EXIT trap and the status ExitStatus reports, unless the trap
// exits itself.
func (s *Shell) CloseOnSignal(sig syscall.Signal) error {
	s.executor.SignalJobs(syscall.SIGHUP)
	return s.close(128 + int(sig))
}

// Close runs the EXIT trap and releases shell resources: the agent session
// and the stores. The shell closes with the status Run ended it with.
func (s *Shell) Close() error {
	return s.close(s.exitStatus)
}

// close closes the shell ending with status (close's callers decide it):
// the EXIT trap runs with $? = status, and the status the shell exits with
// is set for the last time: an exit in the trap replaces status, as in
// bash.
func (s *Shell) close(status int) error {
	s.syncStatus(status)
	if code, exited := s.executor.RunExitTrap(context.Background(), s.out(), s.errOut()); exited {
		status = code
	}
	s.exitStatus = status
	_ = s.agentHandler.Close()
	if s.history != nil {
		_ = s.history.Close()
	}
	if s.learning != nil {
		_ = s.learning.Close()
	}
	if s.predictor != nil {
		_ = s.predictor.Close()
	}
	return nil
}

// recordCommand saves a command to history and returns its id, 0 when it
// was not recorded.
func (s *Shell) recordCommand(line string, exitCode int, duration time.Duration) int64 {
	return s.recordCommandAt(line, exitCode, duration, time.Now())
}

// recordCommandAt is recordCommand with the time history orders the line by.
func (s *Shell) recordCommandAt(line string, exitCode int, duration time.Duration, at time.Time) int64 {
	if s.history == nil {
		return 0
	}

	// Parse for sudo
	sudoResult := history.ParseSudoCommand(line)

	cwd, _ := os.Getwd()
	gitBranch := gitBranchAt(cwd)

	cmd := history.Command{
		Command:    sudoResult.Command,
		Cwd:        cwd,
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		Timestamp:  at,
		GitBranch:  gitBranch,
		IsSudo:     sudoResult.IsSudo,
		SudoUser:   sudoResult.SudoUser,
		RawCommand: sudoResult.RawCommand,
	}

	id, err := s.history.Add(cmd)
	if err != nil {
		return 0
	}
	return id
}

// getDataDir returns the data directory for hash.
func getDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "hash")
}

func configuredHistoryPath(cfg *config.Config) string {
	if cfg != nil && cfg.History.Path != "" {
		return expandUserPath(cfg.History.Path)
	}
	return filepath.Join(getDataDir(), "history.db")
}

func expandUserPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		if home != "" {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// getConfigDir returns the config directory for hash.
func getConfigDir() string {
	return config.Dir()
}

// History returns the history store for use by builtins.
func (s *Shell) History() *history.Store {
	return s.history
}

// Learning returns the learning store for use by builtins.
func (s *Shell) Learning() *learning.FixStore {
	return s.learning
}

// runChpwdHook runs the configured chpwd hooks if the working directory
// changed since they last ran.
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
	// Each hook is a line of the shell (executeLine): it prints to the
	// terminal and Ctrl+C interrupts it. Its status is not the user's
	// line's: handleLine sets $? after the hooks.
	for _, cmd := range s.config.Shell.Hooks.Chpwd {
		_, err := s.executeLine(ctx, cmd, s.out(), s.errOut())
		if err != nil {
			fmt.Fprintf(s.errOut(), "hash: chpwd hook failed: %v\n", err)
		}
	}
}

// handleToolPermission prompts the user to approve an agent tool call.
// Runs on the agent's permission goroutine; ctx is the prompt turn's
// context, and cancellation aborts the prompt with a deny. Inside a ?? turn
// the turn gives the prompt the screen and takes it back; a builtin asking
// the agent (completions generate) gets the bare prompt.
func (s *Shell) handleToolPermission(ctx context.Context, req agent.ToolPermissionRequest) (allow, always bool) {
	s.refreshProjectAllowlist()
	if s.allowlist != nil && s.allowlist.IsAllowed(req.Command) {
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

	t := s.turn.Load()
	// Claim the next key before the prompt is drawn: a quick answer typed
	// as it appears must not be swallowed as a stray keystroke. When no key
	// can answer, no prompt is drawn: the tool is denied, saying why.
	waitKey, release, canAsk := s.claimPermissionKey(t)
	if !canAsk {
		t.noteDenied(req.Command)
		t.deniedUnasked(req.ToolCallID, permissionWaitingFor(req))
		return false, false
	}
	if !s.showPermissionPrompt(t, req) {
		release()
		return false, false // the turn is over: no one is there to answer
	}

	answer := permissionAnswerForKey(waitKey(ctx))
	if answer == permissionCancel && t != nil {
		t.interrupt() // Ctrl+C cancels the turn, the tool with it
	}
	allow = answer == permissionAllow || answer == permissionAlwaysAllow
	always = answer == permissionAlwaysAllow

	if always && s.allowlist != nil {
		s.refreshProjectAllowlist()
		_ = s.allowlist.Allow(req.Command)
	}

	trace.AgentHigh("tool_permission", map[string]any{
		"command":  req.Command,
		"tool":     req.ToolName,
		"decision": map[bool]string{true: "allow", false: "deny"}[allow],
	})

	if t == nil {
		if s.agentOutput != nil {
			s.agentOutput.ClearPermissionPrompt()
		}
		return allow, always
	}
	if !allow {
		t.noteDenied(req.Command)
	}
	t.exitPermission(req.ToolCallID, allow)
	return allow, always
}

// showPermissionPrompt draws the prompt for req. Inside a turn the turn
// gives it the screen, and reports false when the turn no longer waits on
// the agent.
func (s *Shell) showPermissionPrompt(t *liveTurn, req agent.ToolPermissionRequest) bool {
	waitingFor := permissionWaitingFor(req)
	render := func() {
		if s.agentOutput != nil {
			s.agentOutput.RenderPermissionPrompt(req.Command, req.ToolName, s.colorPalette.Primary)
			s.out().Sync()
		}
	}
	if t == nil {
		render()
		return true
	}
	return t.enterPermission(waitingFor, render)
}

// permissionWaitingFor is what a permission prompt for req waits on, for
// the status and the rows that name it.
func permissionWaitingFor(req agent.ToolPermissionRequest) string {
	if req.Command != "" {
		return req.Command
	}
	return req.ToolName
}

// claimPermissionKey claims the key that answers a permission prompt and
// returns how to wait for it, and how to give it up when the prompt is not
// drawn after all: the turn's key watcher inside a turn, the shell's key
// reader outside one. It reports false when no key can answer (claimKey).
func (s *Shell) claimPermissionKey(t *liveTurn) (wait func(context.Context) byte, release func(), ok bool) {
	note := func() {
		if s.agentOutput != nil {
			s.agentOutput.NoteTooQuick()
		}
	}
	if t != nil {
		return t.claimKey(note)
	}
	return func(ctx context.Context) byte { return s.keyboard().permissionKey(ctx, note) }, func() {}, true
}

func (s *Shell) refreshProjectAllowlist() {
	if s.allowlist == nil {
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	_ = s.allowlist.SetProjectDir(cwd)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// permissionAnswer is what a key says to a tool permission prompt.
type permissionAnswer int

const (
	permissionDeny        permissionAnswer = iota // n, Esc, or anything else
	permissionAllow                               // y: this once
	permissionAlwaysAllow                         // a: and remember it
	permissionCancel                              // Ctrl+C: cancel the whole turn
)

// permissionAnswerForKey maps a key pressed at a permission prompt to its
// answer. Only explicit y/a allow: Enter and newline deny, so a stale
// keystroke from the command submission never approves a tool. Ctrl+C
// cancels the turn, as it does everywhere else in one; it arrives as a key
// when the prompt reads stdin with signals off.
func permissionAnswerForKey(key byte) permissionAnswer {
	switch key {
	case 'y', 'Y':
		return permissionAllow
	case 'a', 'A':
		return permissionAlwaysAllow
	case 0x03:
		return permissionCancel
	default:
		return permissionDeny
	}
}

// ExitStatus is the status hash exits with once Run returns nil: the one
// the shellExit that ended the loop carried (exit's, or the last line's on
// Ctrl+D).
func (s *Shell) ExitStatus() int { return s.exitStatus }
