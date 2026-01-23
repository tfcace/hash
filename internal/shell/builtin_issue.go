package shell

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/tfcace/hash/internal/version"
)

const issueTemplate = `%s

## Description
<!-- Describe what happened and what you expected -->


## Context
<!-- Review and remove any sensitive information -->
- Hash: %s
- OS: %s/%s
- Terminal: %s

## Last Command
<!-- Remove if not relevant -->
- Command: %s
- Exit code: %d
- Working directory: %s

## Stderr
` + "```" + `
%s
` + "```" + `

## Additional Info

`

// builtinIssue handles the issue command for submitting GitHub issues.
func (s *Shell) builtinIssue(args []string) error {
	// Parse flags
	var title string
	useLast := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--last", "-l":
			useLast = true
		case "--help", "-h":
			return s.issueHelp()
		default:
			if !strings.HasPrefix(args[i], "-") {
				title = strings.Join(args[i:], " ")
				i = len(args) // Break out of loop
			}
		}
	}

	// Build context
	ctx := s.buildIssueContext(useLast)

	// Create temp file with template
	content := fmt.Sprintf(issueTemplate,
		title,
		s.getVersionString(),
		runtime.GOOS, runtime.GOARCH,
		s.getTerminal(),
		ctx.command,
		ctx.exitCode,
		ctx.cwd,
		ctx.stderr,
	)

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "hash-issue-*.md")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write template: %w", err)
	}
	tmpFile.Close()

	// Get file info for comparison
	origInfo, _ := os.Stat(tmpPath)
	origContent, _ := os.ReadFile(tmpPath)

	// Open editor
	editor := s.findEditor()
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}

	// Check if file was modified
	newInfo, _ := os.Stat(tmpPath)
	newContent, _ := os.ReadFile(tmpPath)

	if origInfo.ModTime().Equal(newInfo.ModTime()) || bytes.Equal(origContent, newContent) {
		fmt.Println("Aborting issue (no changes)")
		return nil
	}

	// Check if content is essentially empty (just template)
	if s.isEmptyIssue(string(newContent)) {
		fmt.Println("Aborting issue (empty content)")
		return nil
	}

	// Parse the edited content
	issueTitle, issueBody := s.parseIssueContent(string(newContent))
	if issueTitle == "" {
		fmt.Println("Aborting issue (no title)")
		return nil
	}

	// Submit via gh CLI
	return s.submitIssue(issueTitle, issueBody)
}

type issueContext struct {
	command  string
	exitCode int
	cwd      string
	stderr   string
}

func (s *Shell) buildIssueContext(useLast bool) issueContext {
	ctx := issueContext{
		command:  "(none)",
		exitCode: 0,
		cwd:      "(unknown)",
		stderr:   "(none)",
	}

	if useLast || s.lastCommand != "" {
		if s.lastCommand != "" {
			ctx.command = "`" + s.lastCommand + "`"
		}
		ctx.exitCode = s.lastExitCode
		if s.lastCwd != "" {
			// Shorten home directory
			ctx.cwd = s.shortenPath(s.lastCwd)
		}
		if s.lastStderr != "" {
			ctx.stderr = s.lastStderr
		}
	}

	return ctx
}

func (s *Shell) shortenPath(path string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func (s *Shell) findEditor() string {
	// Check $EDITOR first
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}

	// Fallback chain: hx -> nvim -> nano
	editors := []string{"hx", "nvim", "nano"}
	for _, ed := range editors {
		if path, err := exec.LookPath(ed); err == nil {
			return path
		}
	}

	return "nano" // Last resort
}

func (s *Shell) getVersionString() string {
	return version.String()
}

func (s *Shell) getTerminal() string {
	if term := os.Getenv("TERM_PROGRAM"); term != "" {
		return term
	}
	if term := os.Getenv("TERM"); term != "" {
		return term
	}
	return "unknown"
}

func (s *Shell) isEmptyIssue(content string) bool {
	// Remove HTML comments
	re := regexp.MustCompile(`<!--.*?-->`)
	content = re.ReplaceAllString(content, "")

	// Remove template sections
	content = strings.ReplaceAll(content, "## Description", "")
	content = strings.ReplaceAll(content, "## Context", "")
	content = strings.ReplaceAll(content, "## Last Command", "")
	content = strings.ReplaceAll(content, "## Stderr", "")
	content = strings.ReplaceAll(content, "## Additional Info", "")

	// Check if there's any substantial content
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "```") {
			// Check if it's not just context values
			if !strings.HasPrefix(line, "- Hash:") &&
				!strings.HasPrefix(line, "- OS:") &&
				!strings.HasPrefix(line, "- Terminal:") &&
				!strings.HasPrefix(line, "- Command:") &&
				!strings.HasPrefix(line, "- Exit code:") &&
				!strings.HasPrefix(line, "- Working directory:") {
				return false
			}
		}
	}
	return true
}

func (s *Shell) parseIssueContent(content string) (title, body string) {
	// Remove HTML comments
	re := regexp.MustCompile(`<!--.*?-->`)
	content = re.ReplaceAllString(content, "")

	lines := strings.Split(content, "\n")

	// First non-empty line is title
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			title = line
			body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			break
		}
	}

	return title, body
}

func (s *Shell) submitIssue(title, body string) error {
	// Check if gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found - install from https://cli.github.com")
	}

	// Create issue
	cmd := exec.Command("gh", "issue", "create",
		"--repo", "tfcace/hash",
		"--title", title,
		"--body", body,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create issue: %w", err)
	}

	return nil
}

func (s *Shell) issueHelp() error {
	fmt.Println(`Usage: issue [OPTIONS] [TITLE]

Submit an issue to the Hash GitHub repository.

Options:
  --last, -l    Pre-fill with context from last command
  --help, -h    Show this help

Examples:
  issue "shell crashes on startup"
  issue --last
  issue`)
	return nil
}
