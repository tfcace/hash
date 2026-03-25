package shell

import "testing"

func TestClassifyCommand(t *testing.T) {
	tests := []struct {
		command string
		want    CommandRisk
	}{
		// Read-only
		{"grep -r foo .", CommandRiskReadOnly},
		{"find . -name '*.go'", CommandRiskReadOnly},
		{"cat README.md", CommandRiskReadOnly},
		{"head -n 10 file.txt", CommandRiskReadOnly},
		{"tail -f log.txt", CommandRiskReadOnly},
		{"ls -la", CommandRiskReadOnly},
		{"wc -l *.go", CommandRiskReadOnly},
		{"file main.go", CommandRiskReadOnly},
		{"stat main.go", CommandRiskReadOnly},
		{"diff a.go b.go", CommandRiskReadOnly},
		{"which go", CommandRiskReadOnly},
		{"pwd", CommandRiskReadOnly},
		{"echo hello", CommandRiskGeneral},
		{"env", CommandRiskReadOnly},
		{"git status", CommandRiskReadOnly},
		{"git log", CommandRiskReadOnly},
		{"git diff", CommandRiskReadOnly},
		{"jj log", CommandRiskReadOnly},
		{"jj diff", CommandRiskReadOnly},
		{"jj status", CommandRiskReadOnly},

		// Test commands
		{"go test ./...", CommandRiskTest},
		{"go test -run TestFoo ./internal/...", CommandRiskTest},
		{"pytest", CommandRiskTest},
		{"pytest tests/", CommandRiskTest},
		{"npm test", CommandRiskTest},
		{"npm run test", CommandRiskTest},
		{"cargo test", CommandRiskTest},
		{"make test", CommandRiskTest},
		{"make check", CommandRiskTest},

		// Destructive
		{"rm file.go", CommandRiskDestructive},
		{"rm -rf /tmp/dir", CommandRiskDestructive},
		{"rmdir old", CommandRiskDestructive},
		{"kill -9 1234", CommandRiskDestructive},
		{"killall node", CommandRiskDestructive},
		{"chmod 000 secret", CommandRiskDestructive},
		{"chown root file", CommandRiskDestructive},
		{"truncate -s 0 log", CommandRiskDestructive},
		{"git push", CommandRiskDestructive},
		{"git reset --hard", CommandRiskDestructive},

		// General (everything else)
		{"go build ./...", CommandRiskGeneral},
		{"npm install", CommandRiskGeneral},
		{"docker ps", CommandRiskReadOnly},
		{"curl https://example.com", CommandRiskGeneral},
		{"mkdir newdir", CommandRiskGeneral},
		{"mv a.go b.go", CommandRiskGeneral},
		{"cp a.go b.go", CommandRiskGeneral},
		{"sed -i 's/foo/bar/g' file.go", CommandRiskGeneral},

		// Sudo and prefix stripping
		{"sudo rm -rf /", CommandRiskDestructive},
		{"sudo cat file", CommandRiskReadOnly},
		{"sudo go test ./...", CommandRiskTest},
		{"env FOO=bar rm file", CommandRiskDestructive},
		{"nice git push origin main", CommandRiskDestructive},
		{"nohup ls -la", CommandRiskReadOnly},
		{"sudo env VAR=1 kill 1234", CommandRiskDestructive},

		// Edge cases
		{"", CommandRiskGeneral},
		{"  go test ./...  ", CommandRiskTest},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := ClassifyCommand(tt.command)
			if got != tt.want {
				t.Errorf("ClassifyCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
