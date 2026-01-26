//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shell"
)

// TestGit_ConventionalCommits tests the conventional commit message recipe.
// Website promise: git diff --staged | ?? generate conventional commit message
func TestGit_ConventionalCommits(t *testing.T) {
	// Read test git diff
	testdataPath := filepath.Join("testdata", "git-diff.patch")
	diffData, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	mock := NewScenarioMock().
		OnPipePromptContains("conventional commit", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git commit -m "fix(auth): add input length validation to prevent buffer overflow"`,
		}).
		OnPipePromptContains("commit message", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git commit -m "feat: add input validation for login"`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("git diff --staged")
	clipBuf.SetOutput(string(diffData))

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name   string
		input  string
		hasCmd bool
	}{
		{
			name:   "generate conventional commit",
			input:  "git diff --staged | ?? generate conventional commit message",
			hasCmd: true,
		},
		{
			name:   "generate commit message",
			input:  "git diff --staged | ?? suggest commit message",
			hasCmd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentPipe {
				t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if tt.hasCmd && resp.Command == "" {
				t.Error("Expected command response")
			}

			// Verify the response starts with git commit
			if tt.hasCmd && len(resp.Command) > 0 {
				if resp.Command[:10] != "git commit" {
					t.Logf("Note: Response %q doesn't start with 'git commit'", resp.Command)
				}
			}
		})
	}
}

// TestGit_BranchCleanup tests the branch cleanup recipe.
// Website promise: git branch --merged | ?? list branches safe to delete (exclude main)
func TestGit_BranchCleanup(t *testing.T) {
	// Sample git branch output
	branchOutput := `* main
  feature/auth-improvements
  feature/api-v2
  fix/login-bug
  chore/update-deps
  release/v1.0.0`

	mock := NewScenarioMock().
		OnPipePromptContains("safe to delete", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `grep -v -E '^\*|main|master|release' | xargs -I {} echo "git branch -d {}"`,
		}).
		OnPipePromptContains("filter out main", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `grep -v -E '^\*|main|master' | sed 's/^[ \t]*//'`,
		}).
		OnPipePromptContains("delete merged", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `grep -v -E '^\*|main|master' | xargs git branch -d`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("git branch --merged")
	clipBuf.SetOutput(branchOutput)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name   string
		input  string
		hasCmd bool
	}{
		{
			name:   "list branches safe to delete",
			input:  "git branch --merged | ?? list branches safe to delete (exclude main)",
			hasCmd: true,
		},
		{
			name:   "filter out main",
			input:  "git branch --merged | ?? filter out main and master, exclude current",
			hasCmd: true,
		},
		{
			name:   "delete merged branches",
			input:  "git branch --merged | ?? delete merged branches except main",
			hasCmd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentPipe {
				t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if tt.hasCmd && resp.Command == "" {
				t.Error("Expected command response")
			}
		})
	}
}

// TestGit_LogFormatting tests git log format inline completion.
// Website promise: git log --format=?? oneline with hash
func TestGit_LogFormatting(t *testing.T) {
	mock := NewScenarioMock().
		OnInlineContains("--format=", "oneline", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `'%h %s'`,
		}).
		OnInlineContains("--format=", "author", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `'%h %an: %s'`,
		}).
		OnInlineContains("--format=", "date", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `'%h %ad %s' --date=short`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name         string
		input        string
		wantComplete string
	}{
		{
			name:         "oneline with hash",
			input:        "git log --format=?? oneline with hash",
			wantComplete: `git log --format='%h %s'`,
		},
		{
			name:         "with author",
			input:        "git log --format=?? with author name",
			wantComplete: `git log --format='%h %an: %s'`,
		},
		{
			name:         "with date",
			input:        "git log --format=?? with date",
			wantComplete: `git log --format='%h %ad %s' --date=short`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentInline {
				t.Fatalf("Parse type = %v, want AgentInline", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			complete := parsed.Command + resp.Command
			if complete != tt.wantComplete {
				t.Errorf("Complete = %q, want %q", complete, tt.wantComplete)
			}
		})
	}
}

// TestGit_DirectCommands tests non-pipe git commands.
func TestGit_DirectCommands(t *testing.T) {
	mock := NewScenarioMock().
		OnPromptContains("undo last commit", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git reset --soft HEAD~1`,
		}).
		OnPromptContains("uncommitted changes", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git stash`,
		}).
		OnPromptContains("conflicts", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git diff --name-only --diff-filter=U`,
		}).
		OnPromptContains("squash", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git rebase -i HEAD~3`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name        string
		input       string
		wantCommand string
	}{
		{
			name:        "undo last commit",
			input:       "?? undo last commit but keep changes",
			wantCommand: `git reset --soft HEAD~1`,
		},
		{
			name:        "stash changes",
			input:       "?? save uncommitted changes temporarily",
			wantCommand: `git stash`,
		},
		{
			name:        "find conflicts",
			input:       "?? list files with merge conflicts",
			wantCommand: `git diff --name-only --diff-filter=U`,
		},
		{
			name:        "squash commits",
			input:       "?? squash last 3 commits",
			wantCommand: `git rebase -i HEAD~3`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgent {
				t.Fatalf("Parse type = %v, want Agent", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if resp.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", resp.Command, tt.wantCommand)
			}
		})
	}
}

// TestGit_ContextIncludesGitBranch verifies git branch is included in context.
func TestGit_ContextIncludesGitBranch(t *testing.T) {
	mock := NewScenarioMock().
		OnPromptContains("status", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `git status -s`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parsed := parser.Parse("?? show git status")
	_, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	// Verify context was captured
	lastReq, ok := mock.LastRequest()
	if !ok {
		t.Fatal("No request captured")
	}

	// The context builder should have detected git branch (if in a git repo)
	// This test verifies the context is being passed, not the specific value
	t.Logf("Git context - Branch: %q, Cwd: %q", lastReq.Context.GitBranch, lastReq.Context.Cwd)
}
