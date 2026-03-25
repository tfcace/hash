package shell

import "strings"

// CommandRisk represents the risk level of a shell command.
type CommandRisk int

const (
	CommandRiskReadOnly    CommandRisk = iota // grep, cat, ls, find, etc.
	CommandRiskTest                           // go test, pytest, npm test, etc.
	CommandRiskGeneral                        // build, install, curl, mkdir, etc.
	CommandRiskDestructive                    // rm, kill, chmod, git push, etc.
)

func (r CommandRisk) String() string {
	switch r {
	case CommandRiskReadOnly:
		return "read-only"
	case CommandRiskTest:
		return "test"
	case CommandRiskGeneral:
		return "general"
	case CommandRiskDestructive:
		return "destructive"
	default:
		return "unknown"
	}
}

// readOnlyCommands are commands that only read data.
var readOnlyCommands = map[string]bool{
	"grep": true, "rg": true, "ag": true, "find": true, "fd": true,
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"ls": true, "tree": true, "wc": true, "file": true, "stat": true,
	"diff": true, "which": true, "where": true, "type": true,
	"pwd": true, "env": true, "printenv": true,
	"whoami": true, "hostname": true, "uname": true, "date": true,
	"du": true, "df": true, "free": true, "uptime": true, "id": true,
	"sort": true, "uniq": true, "cut": true, "tr": true,
	"sha256sum": true, "md5sum": true, "sha1sum": true,
}

// readOnlySubcommands are command+subcommand pairs that are read-only.
var readOnlySubcommands = map[string]bool{
	"git status": true, "git log": true, "git diff": true, "git show": true,
	"git branch": true, "git tag": true, "git remote": true,
	"jj log": true, "jj diff": true, "jj status": true, "jj show": true,
	"docker ps": true, "docker images": true, "docker inspect": true,
	"kubectl get": true, "kubectl describe": true, "kubectl logs": true,
}

// destructiveCommands are commands that delete, kill, or irreversibly modify.
var destructiveCommands = map[string]bool{
	"rm": true, "rmdir": true, "kill": true, "killall": true, "pkill": true,
	"chmod": true, "chown": true, "chgrp": true, "truncate": true, "shred": true,
}

// destructiveSubcommands are command+subcommand pairs that are destructive.
var destructiveSubcommands = map[string]bool{
	"git push": true, "git reset": true, "git clean": true,
	"jj abandon": true,
	"docker rm": true, "docker rmi": true,
	"kubectl delete": true,
}

// testPatterns are command prefixes that indicate test execution.
var testPatterns = []string{
	"go test",
	"pytest", "python -m pytest",
	"npm test", "npm run test", "npx jest", "npx vitest",
	"yarn test",
	"cargo test",
	"make test", "make check",
	"bundle exec rspec", "rspec",
	"mvn test", "gradle test",
}

// commandPrefixes are wrappers that don't change the nature of the command.
var commandPrefixes = map[string]bool{
	"sudo": true, "nice": true, "nohup": true,
	"time": true, "command": true,
}

// ClassifyCommand determines the risk level of a shell command.
func ClassifyCommand(command string) CommandRisk {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return CommandRiskGeneral
	}

	// Strip command-wrapping prefixes (sudo, env, nice, nohup, time, command).
	// env is special: it takes KEY=VALUE pairs before the actual command.
	for {
		fields := strings.Fields(cmd)
		if len(fields) == 0 {
			return CommandRiskGeneral
		}
		first := strings.ToLower(fields[0])
		if commandPrefixes[first] {
			cmd = strings.TrimSpace(strings.TrimPrefix(cmd, fields[0]))
			continue
		}
		if first == "env" && len(fields) > 1 {
			// Strip "env" and any KEY=VALUE args
			cmd = strings.TrimSpace(strings.TrimPrefix(cmd, fields[0]))
			for {
				f := strings.Fields(cmd)
				if len(f) == 0 {
					return CommandRiskGeneral
				}
				if strings.Contains(f[0], "=") {
					cmd = strings.TrimSpace(strings.TrimPrefix(cmd, f[0]))
				} else {
					break
				}
			}
			continue
		}
		break
	}

	// Check test patterns first (multi-word matches)
	cmdLower := strings.ToLower(cmd)
	for _, pattern := range testPatterns {
		if cmdLower == pattern || strings.HasPrefix(cmdLower, pattern+" ") {
			return CommandRiskTest
		}
	}

	// Extract first word and first two words
	fields := strings.Fields(cmd)
	first := strings.ToLower(fields[0])
	var firstTwo string
	if len(fields) >= 2 {
		firstTwo = first + " " + strings.ToLower(fields[1])
	}

	// Check destructive subcommands
	if firstTwo != "" && destructiveSubcommands[firstTwo] {
		return CommandRiskDestructive
	}

	// Check destructive commands
	if destructiveCommands[first] {
		return CommandRiskDestructive
	}

	// Check read-only subcommands
	if firstTwo != "" && readOnlySubcommands[firstTwo] {
		return CommandRiskReadOnly
	}

	// Check read-only commands
	if readOnlyCommands[first] {
		return CommandRiskReadOnly
	}

	return CommandRiskGeneral
}
