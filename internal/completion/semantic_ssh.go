package completion

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
)

// SSHHandler provides completions for ssh, scp, and sftp commands.
type SSHHandler struct {
	readFile func(path string) ([]string, error) // injectable for testing
}

// NewSSHHandler creates an SSH completion handler.
func NewSSHHandler() *SSHHandler {
	return &SSHHandler{readFile: readLines}
}

// Commands returns the commands this handler supports.
func (h *SSHHandler) Commands() []string {
	return []string{"ssh", "scp", "sftp"}
}

// Complete returns SSH host completions.
func (h *SSHHandler) Complete(ctx context.Context, args []string, current string) Result {
	// Skip if current arg looks like a flag
	if strings.HasPrefix(current, "-") {
		return Result{}
	}

	hosts := h.collectHosts()
	return prefixFilterItems(hosts, current)
}

func (h *SSHHandler) collectHosts() []string {
	seen := make(map[string]bool)
	var hosts []string

	// Parse ~/.ssh/config
	home, err := os.UserHomeDir()
	if err == nil {
		configHosts := h.parseSSHConfig(filepath.Join(home, ".ssh", "config"))
		for _, host := range configHosts {
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}

		// Parse ~/.ssh/known_hosts
		knownHosts := h.parseKnownHosts(filepath.Join(home, ".ssh", "known_hosts"))
		for _, host := range knownHosts {
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}

	return hosts
}

func (h *SSHHandler) parseSSHConfig(path string) []string {
	lines, err := h.readFile(path)
	if err != nil {
		return nil
	}

	var hosts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		// Match "Host <pattern>" directives
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "host ") {
			continue
		}
		parts := strings.Fields(line)
		for _, pattern := range parts[1:] {
			// Skip wildcard patterns
			if strings.ContainsAny(pattern, "*?") {
				continue
			}
			hosts = append(hosts, pattern)
		}
	}
	return hosts
}

func (h *SSHHandler) parseKnownHosts(path string) []string {
	lines, err := h.readFile(path)
	if err != nil {
		return nil
	}

	var hosts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
			continue
		}
		// Format: hostname[,hostname...] keytype key
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// First field is comma-separated hostnames
		for _, host := range strings.Split(fields[0], ",") {
			host = strings.TrimPrefix(host, "[")
			host = strings.TrimSuffix(host, "]")
			// Skip hashed entries
			if strings.HasPrefix(host, "|") {
				continue
			}
			// Remove port suffix
			if idx := strings.LastIndex(host, ":"); idx > 0 {
				host = host[:idx]
			}
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// readLines reads a file into lines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
