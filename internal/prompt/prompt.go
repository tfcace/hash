package prompt

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/tfcace/hash/internal/tilde"
)

// Config configures the prompt behavior.
type Config struct {
	Mode         string // "starship" (default), "built-in", "none"
	StarshipPath string // Optional explicit path
	Alignment    string // "left" or "right"
}

// PromptContext provides context for prompt generation.
type PromptContext struct {
	Cwd        string
	ExitCode   int
	DurationMs int64
	Jobs       int
}

// engine is the configured prompt engine.
type engine int

const (
	// engineStarship runs starship once starshipLookup finds it; meanwhile,
	// and when starship fails, the built-in prompt is shown.
	engineStarship engine = iota
	engineBuiltin         // cwd and a colored prompt character
	engineNone            // a bare prompt character, no cwd or colors
)

func engineForMode(mode string) engine {
	switch mode {
	case "starship", "":
		return engineStarship
	case "none":
		return engineNone
	default: // "built-in", and unknown modes
		return engineBuiltin
	}
}

// starshipLookup finds the starship binary. It has two states: found (path
// set, final) and not found yet, in which every use looks again, since
// startup files may still extend PATH. The first prompt and the palette are
// drawn concurrently, so the lookup is locked.
type starshipLookup struct {
	explicit string // configured path, tried before PATH

	mu   sync.Mutex
	path string // "" until found
}

// find returns the starship binary, looking it up again if it was not found
// yet.
func (l *starshipLookup) find() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" {
		l.path = findStarship(l.explicit)
	}
	return l.path, l.path != ""
}

// Prompt generates shell prompts.
type Prompt struct {
	config   Config
	engine   engine
	starship starshipLookup
}

// New creates a new Prompt generator. The starship lookup is deferred to the
// first prompt so startup files can set up PATH first.
func New(cfg Config) *Prompt {
	return &Prompt{config: cfg, engine: engineForMode(cfg.Mode), starship: starshipLookup{explicit: cfg.StarshipPath}}
}

// Generate returns the prompt string.
func (p *Prompt) Generate(ctx PromptContext) string {
	switch p.engine {
	case engineStarship:
		if path, ok := p.starship.find(); ok {
			return p.starshipPrompt(path, ctx)
		}
		return p.builtinPrompt(ctx)
	case engineNone:
		return "❯ "
	default:
		return p.builtinPrompt(ctx)
	}
}

func findStarship(explicit string) string {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit
		}
	}

	path, err := exec.LookPath("starship")
	if err == nil {
		return path
	}

	return ""
}

func (p *Prompt) starshipPrompt(path string, ctx PromptContext) string {
	cmd := exec.Command(path, "prompt",
		"--status", strconv.Itoa(ctx.ExitCode),
		"--cmd-duration", strconv.FormatInt(ctx.DurationMs, 10),
		"--jobs", strconv.Itoa(ctx.Jobs),
	)
	cmd.Env = append(os.Environ(), "STARSHIP_SHELL=hash")

	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return p.builtinPrompt(ctx)
	}

	prompt := out.String()
	prompt = stripCursorPositioning(prompt)
	prompt = stripClearSequences(prompt)

	return prompt
}

// GenerateMultiLine returns the prompt split into prefix (printed before the
// line editor starts) and the last line (given to the line editor), which is
// the part it redraws.
func (p *Prompt) GenerateMultiLine(ctx PromptContext) (prefix, prompt string) {
	full := p.Generate(ctx)

	// Find the last newline - everything before it is the prefix,
	// everything after is the readline prompt
	lastNewline := strings.LastIndex(full, "\n")
	if lastNewline == -1 {
		// Single line prompt, no prefix needed
		return "", full
	}

	return full[:lastNewline+1], full[lastNewline+1:]
}

func (p *Prompt) builtinPrompt(ctx PromptContext) string {
	cwd := tilde.Abbrev(ctx.Cwd)

	char := "❯"
	color := "\033[32m" // green
	if ctx.ExitCode != 0 {
		color = "\033[31m" // red
	}
	reset := "\033[0m"

	return fmt.Sprintf("%s %s%s%s ", cwd, color, char, reset)
}

// GenerateForPalette runs starship with the given exit code and returns raw output
// suitable for palette extraction. Returns "" if starship is not available.
func (p *Prompt) GenerateForPalette(exitCode int) string {
	if p.engine != engineStarship {
		return ""
	}
	path, ok := p.starship.find()
	if !ok {
		return ""
	}
	return runStarship(path, exitCode)
}

// cursorPosRegex matches cursor positioning escape sequences.
// These include:
// - CSI n G  (cursor horizontal absolute)
// - CSI n C  (cursor forward)
// - CSI n D  (cursor back)
// - CSI n ; m H (cursor position)
// - CSI n ; m f (cursor position alt)
// - CSI s/u  (save/restore cursor)
var cursorPosRegex = regexp.MustCompile(`\x1b\[[\d;]*[GCDHfsu]`)

// stripCursorPositioning removes cursor positioning escape sequences
// that break readline's line width calculations.
func stripCursorPositioning(s string) string {
	return cursorPosRegex.ReplaceAllString(s, "")
}

// clearSeqRegex matches screen/line clearing escape sequences.
var clearSeqRegex = regexp.MustCompile(`\x1b\[[0-3]?[JK]`)

// stripClearSequences removes screen/line clearing escape sequences.
func stripClearSequences(s string) string {
	return clearSeqRegex.ReplaceAllString(s, "")
}
