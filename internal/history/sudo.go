package history

import (
	"strings"
)

// SudoResult contains parsed sudo command info.
type SudoResult struct {
	IsSudo     bool
	Command    string
	SudoUser   string
	RawCommand string
}

// ParseSudoCommand detects and parses sudo/doas commands.
func ParseSudoCommand(raw string) SudoResult {
	raw = strings.TrimSpace(raw)

	// Check for sudo
	if strings.HasPrefix(raw, "sudo ") {
		return parseSudo(raw)
	}

	// Check for doas
	if strings.HasPrefix(raw, "doas ") {
		return parseDoas(raw)
	}

	return SudoResult{
		IsSudo:     false,
		Command:    raw,
		RawCommand: raw,
	}
}

// parseSudo parses a sudo command.
func parseSudo(raw string) SudoResult {
	result := SudoResult{
		IsSudo:     true,
		RawCommand: raw,
		SudoUser:   "root", // Default
	}

	// Remove "sudo " prefix
	rest := strings.TrimPrefix(raw, "sudo ")
	parts := strings.Fields(rest)

	// Parse sudo flags
	cmdStart := 0
	for i := 0; i < len(parts); i++ {
		part := parts[i]

		if strings.HasPrefix(part, "-") {
			// Check for -u/--user flag
			if part == "-u" || part == "--user" {
				if i+1 < len(parts) {
					result.SudoUser = parts[i+1]
					i++ // Skip the user argument
				}
				cmdStart = i + 1
				continue
			}

			// Skip other single-letter flags (-E, -i, -s, etc.)
			if len(part) == 2 {
				cmdStart = i + 1
				continue
			}

			// Handle combined flags like -Eu
			if strings.Contains(part, "u") && len(part) > 2 {
				// -u is combined, next arg is user
				if i+1 < len(parts) && !strings.HasPrefix(parts[i+1], "-") {
					result.SudoUser = parts[i+1]
					i++
				}
			}
			cmdStart = i + 1
			continue
		}

		// Not a flag, this is the command
		cmdStart = i
		break
	}

	if cmdStart < len(parts) {
		result.Command = strings.Join(parts[cmdStart:], " ")
	}

	return result
}

// parseDoas parses a doas command.
func parseDoas(raw string) SudoResult {
	result := SudoResult{
		IsSudo:     true,
		RawCommand: raw,
		SudoUser:   "root",
	}

	// Remove "doas " prefix
	rest := strings.TrimPrefix(raw, "doas ")
	parts := strings.Fields(rest)

	cmdStart := 0
	for i := 0; i < len(parts); i++ {
		part := parts[i]

		if strings.HasPrefix(part, "-") {
			if part == "-u" {
				if i+1 < len(parts) {
					result.SudoUser = parts[i+1]
					i++
				}
				cmdStart = i + 1
				continue
			}
			cmdStart = i + 1
			continue
		}

		cmdStart = i
		break
	}

	if cmdStart < len(parts) {
		result.Command = strings.Join(parts[cmdStart:], " ")
	}

	return result
}
