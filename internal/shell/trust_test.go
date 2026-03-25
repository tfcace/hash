package shell

import "testing"

func TestTrustPolicy(t *testing.T) {
	tests := []struct {
		name     string
		tier     string
		toolName string
		command  string
		want     PermissionDecision
	}{
		// suggest tier - deny everything
		{"suggest/read", "suggest", "Read", "/tmp/file.go", PermissionDeny},
		{"suggest/bash-readonly", "suggest", "Bash", "cat file.go", PermissionDeny},
		{"suggest/write", "suggest", "Write", "/tmp/file.go", PermissionDeny},
		{"suggest/bash-general", "suggest", "Bash", "go build", PermissionDeny},

		// assist tier
		{"assist/read", "assist", "Read", "/tmp/file.go", PermissionAllow},
		{"assist/bash-readonly", "assist", "Bash", "cat file.go", PermissionAllow},
		{"assist/bash-test", "assist", "Bash", "go test ./...", PermissionAllow},
		{"assist/bash-general", "assist", "Bash", "go build ./...", PermissionPrompt},
		{"assist/write", "assist", "Write", "/tmp/file.go", PermissionPrompt},
		{"assist/bash-destructive", "assist", "Bash", "rm file.go", PermissionDeny},
		{"assist/bash-git-push", "assist", "Bash", "git push", PermissionDeny},

		// auto tier
		{"auto/read", "auto", "Read", "/tmp/file.go", PermissionAllow},
		{"auto/bash-readonly", "auto", "Bash", "cat file.go", PermissionAllow},
		{"auto/bash-test", "auto", "Bash", "go test ./...", PermissionAllow},
		{"auto/bash-general", "auto", "Bash", "go build ./...", PermissionAllow},
		{"auto/write", "auto", "Write", "/tmp/file.go", PermissionPrompt},
		{"auto/bash-destructive", "auto", "Bash", "rm file.go", PermissionPrompt},

		// Unknown tier falls back to suggest
		{"unknown/read", "invalid", "Read", "/tmp/file.go", PermissionDeny},

		// Unknown tool name defaults to prompt (assist) or deny (suggest)
		{"assist/unknown-tool", "assist", "UnknownTool", "something", PermissionPrompt},
		{"suggest/unknown-tool", "suggest", "UnknownTool", "something", PermissionDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateTrust(tt.tier, tt.toolName, tt.command)
			if got != tt.want {
				t.Errorf("EvaluateTrust(%q, %q, %q) = %v, want %v",
					tt.tier, tt.toolName, tt.command, got, tt.want)
			}
		})
	}
}
