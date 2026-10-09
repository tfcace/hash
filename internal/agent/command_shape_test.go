package agent

import "testing"

func TestCommandFromResponse(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{"bare command", "find . -name '*.go'", "find . -name '*.go'", true},
		{"lead-in then indented command", "The command is:\n  find . -name '*.go' -mtime 0", "find . -name '*.go' -mtime 0", true},
		{"lead-in with sentences then command", "No context here. The request: find Go files. A single bare command:\nfind . -name '*.go' -mtime 0", "find . -name '*.go' -mtime 0", true},
		{"lead-in then fenced command", "A single bare command:\n```bash\nfind . -name '*.go' -mtime 0\n```", "find . -name '*.go' -mtime 0", true},
		{"fenced bare command", "```sh\nls -la\n```", "ls -la", true},
		{"lead-in then inline-code command", "Run:\n`git status`", "git status", true},
		{"prompt marker stripped", "Try:\n$ git status", "git status", true},
		{"bold lead-in", "**Command:**\nls -la", "ls -la", true},
		{"lead-in without command word is output", "Found these files:\n./main.go", "", false},
		{"lead-in word must match whole", "Files owned by the user:\n/home/me/bin/tool", "", false},
		{"lead-in without colon is output", "Here is what you should run\nls -la", "", false},
		{"trailing explanation keeps output", "The command is:\n  find . -name '*.go'\nThis lists all Go files.", "", false},
		{"second line prose is output", "The command is:\nThis will list the files.", "", false},
		{"prose stays output", "The largest files are config.db and logs.tar", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := commandFromResponse(tt.text)
			if ok != tt.ok {
				t.Fatalf("commandFromResponse(%q) ok = %v, want %v", tt.text, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("commandFromResponse(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestStreamCollector_LeadInThenCommandIsCommand(t *testing.T) {
	c := NewStreamCollector()
	c.Append("The command is:\n")
	c.Append("  find . -name '*.go' -mtime 0")

	resp := c.Response()
	if resp.Type != ResponseTypeCommand {
		t.Fatalf("expected ResponseTypeCommand, got %v", resp.Type)
	}
	if resp.Command != "find . -name '*.go' -mtime 0" {
		t.Errorf("Command = %q, want the command line only", resp.Command)
	}
}

func TestParseAgentResponse_LeadInThenCommandIsCommand(t *testing.T) {
	resp := parseAgentResponse("Run:\n```sh\ngit status\n```")
	if resp.Type != ResponseTypeCommand {
		t.Fatalf("expected ResponseTypeCommand, got %v", resp.Type)
	}
	if resp.Command != "git status" {
		t.Errorf("Command = %q, want %q", resp.Command, "git status")
	}
}
