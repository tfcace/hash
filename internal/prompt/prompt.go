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
	"sync"
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

// precomputeResult holds a precomputed prompt output for fast reuse.
type precomputeResult struct {
	cwd        string
	output     string
	timeOutput string // raw starship module time output (for patching)
	timeStr    string // extracted HH:MM from timeOutput
}

// Prompt generates shell prompts.
type Prompt struct {
	config       Config
	starshipPath string

	mu          sync.Mutex
	precomputed *precomputeResult
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

func (p *Prompt) starshipPrompt(ctx PromptContext) string {
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

	return prompt
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

// ResolveStarship forces the lazy starship binary lookup.
// Call before concurrent Generate/GenerateForPalette to avoid races.
func (p *Prompt) ResolveStarship() {
	if p.config.Mode == "starship" && p.starshipPath == "" {
		p.starshipPath = p.findStarship(p.config.StarshipPath)
	}
}

// hhmmRe matches HH:MM time strings (used to patch cached time output).
var hhmmRe = regexp.MustCompile(`\d{2}:\d{2}`)

// StartPrecompute begins generating the next prompt in the background,
// assuming exit code 0, zero duration, and the given cwd. The result is
// stored and can be retrieved via TryPrecomputed. The time module output
// is captured separately so it can be patched with the current time at
// render without spawning a subprocess.
func (p *Prompt) StartPrecompute(cwd string) {
	if p.starshipPath == "" {
		return
	}
	go func() {
		ctx := PromptContext{Cwd: cwd, ExitCode: 0}
		var output, timeOutput string
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			output = p.Generate(ctx)
		}()
		go func() {
			defer wg.Done()
			timeOutput = p.renderModule("time")
		}()
		wg.Wait()

		var timeStr string
		if timeOutput != "" {
			timeStr = hhmmRe.FindString(timeOutput)
		}

		p.mu.Lock()
		p.precomputed = &precomputeResult{
			cwd: cwd, output: output,
			timeOutput: timeOutput, timeStr: timeStr,
		}
		p.mu.Unlock()
	}()
}

// TryPrecomputed returns a precomputed prompt if one is available and the
// context matches. The time module is patched inline with the current time
// (no subprocess). Returns ("", false) on miss — caller should Generate
// synchronously.
func (p *Prompt) TryPrecomputed(ctx PromptContext) (string, bool) {
	// Only valid when the precomputed assumptions hold:
	// exit=0, duration below cmd_duration threshold, same cwd.
	if ctx.ExitCode != 0 || ctx.DurationMs >= 100 {
		p.mu.Lock()
		p.precomputed = nil
		p.mu.Unlock()
		return "", false
	}
	p.mu.Lock()
	pc := p.precomputed
	p.precomputed = nil
	p.mu.Unlock()

	if pc == nil || pc.cwd != ctx.Cwd {
		return "", false
	}

	result := pc.output
	// Patch the time module inline if precomputed time differs from now.
	if pc.timeStr != "" && pc.timeOutput != "" {
		now := time.Now().Format("15:04")
		if now != pc.timeStr {
			freshTime := strings.Replace(pc.timeOutput, pc.timeStr, now, 1)
			result = strings.Replace(result, pc.timeOutput, freshTime, 1)
		}
	}
	return result, true
}

// renderModule runs a single starship module and returns its output.
func (p *Prompt) renderModule(name string) string {
	cmd := exec.Command(p.starshipPath, "module", name)
	cmd.Env = append(os.Environ(), "STARSHIP_SHELL=hash")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}

// GenerateForPalette runs starship with the given exit code and returns raw output
// suitable for palette extraction. Returns "" if starship is not available.
func (p *Prompt) GenerateForPalette(exitCode int) string {
	if p.starshipPath == "" {
		return ""
	}
	return runStarship(p.starshipPath, exitCode)
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
