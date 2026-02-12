package prompt

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config configures the prompt behavior.
type Config struct {
	Mode         string // "starship", "built-in", "none"
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

// Prompt generates shell prompts.
type Prompt struct {
	config       Config
	starshipPath string

	// Starship output cache: avoids re-spawning the subprocess when the
	// prompt context (cwd, exit code, duration, jobs) hasn't changed.
	cachedCtx       PromptContext
	cachedResult    string
	cacheValid      bool
	cachedCfgMtime  time.Time // mtime of starship config at cache time
	starshipCfgPath string    // resolved once, empty = not yet resolved
}

// New creates a new Prompt generator.
func New(cfg Config) *Prompt {
	p := &Prompt{config: cfg}

	// If explicit path is provided, use it immediately
	if cfg.Mode == "starship" && cfg.StarshipPath != "" {
		p.starshipPath = p.findStarship(cfg.StarshipPath)
	}
	// Otherwise, defer starship lookup until first prompt generation
	// This allows PATH to be set up by startup files first

	return p
}

// StarshipPath returns the path to the starship binary, if configured.
func (p *Prompt) StarshipPath() string {
	return p.starshipPath
}

// Generate returns the prompt string.
func (p *Prompt) Generate(ctx PromptContext) string {
	switch p.config.Mode {
	case "starship":
		// Lazy lookup: retry finding starship until found (allows PATH to be set up by startup files)
		if p.starshipPath == "" {
			p.starshipPath = p.findStarship(p.config.StarshipPath)
		}
		if p.starshipPath != "" {
			return p.starshipPrompt(ctx)
		}
		// Fall through to built-in if starship not found
		fallthrough
	case "built-in":
		return p.builtinPrompt(ctx)
	default:
		return p.fallbackPrompt(ctx)
	}
}

func (p *Prompt) findStarship(explicit string) string {
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

// starshipConfigMtime returns the modification time of the starship config file.
// Returns zero time if the file doesn't exist or can't be stat'd.
func (p *Prompt) starshipConfigMtime() time.Time {
	if p.starshipCfgPath == "" {
		// Resolve once: $STARSHIP_CONFIG > ~/.config/starship.toml
		if env := os.Getenv("STARSHIP_CONFIG"); env != "" {
			p.starshipCfgPath = env
		} else if home, err := os.UserHomeDir(); err == nil {
			p.starshipCfgPath = filepath.Join(home, ".config", "starship.toml")
		}
	}
	if p.starshipCfgPath == "" {
		return time.Time{}
	}
	info, err := os.Stat(p.starshipCfgPath)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func (p *Prompt) starshipPrompt(ctx PromptContext) string {
	// Return cached result if the context hasn't changed and the starship
	// config file hasn't been modified. The os.Stat costs ~1μs, which is
	// negligible compared to the 60-250ms subprocess it gates.
	cfgMtime := p.starshipConfigMtime()
	if p.cacheValid && p.cachedCtx == ctx && p.cachedCfgMtime.Equal(cfgMtime) {
		return p.cachedResult
	}

	cmd := exec.Command(p.starshipPath, "prompt",
		"--status", strconv.Itoa(ctx.ExitCode),
		"--cmd-duration", strconv.FormatInt(ctx.DurationMs, 10),
		"--jobs", strconv.Itoa(ctx.Jobs),
	)
	cmd.Env = append(os.Environ(), "STARSHIP_SHELL=hash")

	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return p.fallbackPrompt(ctx)
	}

	prompt := out.String()
	prompt = stripCursorPositioning(prompt)
	prompt = stripClearSequences(prompt)

	// Cache the result for next call
	p.cachedCtx = ctx
	p.cachedResult = prompt
	p.cachedCfgMtime = cfgMtime
	p.cacheValid = true

	return prompt
}

// InvalidateCache forces the next Generate call to re-run starship.
// Use after events that change prompt state outside of PromptContext
// (e.g., git branch change detected by chpwd hook).
func (p *Prompt) InvalidateCache() {
	p.cacheValid = false
}

// GenerateMultiLine returns the prompt split into prefix (printed before readline)
// and the actual prompt (given to readline). This is needed because chzyer/readline
// doesn't support multi-line prompts properly.
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
	cwd := ctx.Cwd
	if home := os.Getenv("HOME"); home != "" {
		if rel, err := filepath.Rel(home, cwd); err == nil && len(rel) < len(cwd) {
			cwd = "~/" + rel
		}
	}

	char := "❯"
	color := "\033[32m" // green
	if ctx.ExitCode != 0 {
		color = "\033[31m" // red
	}
	reset := "\033[0m"

	return fmt.Sprintf("%s %s%s%s ", cwd, color, char, reset)
}

func (p *Prompt) fallbackPrompt(ctx PromptContext) string {
	return p.builtinPrompt(ctx)
}

// RightPrompt returns the right-side prompt (if supported).
// Note: Right prompts are not currently used with chzyer/readline
// because it doesn't support them properly.
func (p *Prompt) RightPrompt(ctx PromptContext) string {
	// Disabled: chzyer/readline doesn't support right prompts.
	// The cursor positioning sequences break line editing.
	return ""
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
