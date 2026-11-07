package completion

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SSHHandler provides completions for ssh, scp, and sftp commands.
type SSHHandler struct {
	readFile func(path string) ([]string, error) // injectable for testing
	cacheTTL time.Duration
	hosts    flightCache[[]string]
}

// NewSSHHandler creates an SSH completion handler.
func NewSSHHandler() *SSHHandler {
	return &SSHHandler{
		readFile: readLines,
		cacheTTL: 30 * time.Second,
	}
}

func (h *SSHHandler) setReady(n *readyNotifier) { h.hosts.ready = n }

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

	hosts, status := h.collectHosts(ctx)
	return sourceResult(hosts, status, current)
}

// collectHosts returns the hosts named in ~/.ssh/config and known_hosts.
// Missing files are normal and contribute nothing.
func (h *SSHHandler) collectHosts(ctx context.Context) ([]string, lookupStatus) {
	policy := flightPolicy{ttl: h.cacheTTL, timeout: fileSourceTimeout, wait: sourceWait}
	return h.hosts.get(ctx, "hosts", policy, func(context.Context) ([]string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		seen := make(map[string]bool)
		var hosts []string
		for _, host := range append(
			h.parseSSHConfig(filepath.Join(home, ".ssh", "config")),
			h.parseKnownHosts(filepath.Join(home, ".ssh", "known_hosts"))...,
		) {
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
		return hosts, nil
	})
}

func (h *SSHHandler) parseSSHConfig(path string) []string {
	lines, err := h.readFile(path)
	if err != nil {
		return nil
	}
	return parseSSHConfigLines(lines)
}

func parseSSHConfigLines(lines []string) []string {
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
	return parseKnownHostsLines(lines)
}

func parseKnownHostsLines(lines []string) []string {
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
			// Handle bracketed [addr]:port (IPv6 with explicit port)
			if strings.HasPrefix(host, "[") {
				if end := strings.Index(host, "]"); end >= 0 {
					host = host[1:end]
				}
			} else {
				// Remove port suffix for non-bracketed entries
				if idx := strings.LastIndex(host, ":"); idx > 0 {
					host = host[:idx]
				}
			}
			// Skip hashed entries
			if strings.HasPrefix(host, "|") {
				continue
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
