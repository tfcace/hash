package shell

// PermissionDecision is the result of evaluating a trust policy.
type PermissionDecision int

const (
	PermissionAllow  PermissionDecision = iota // Auto-allow without prompting
	PermissionPrompt                           // Ask the user
	PermissionDeny                             // Auto-deny without prompting
)

func (d PermissionDecision) String() string {
	switch d {
	case PermissionAllow:
		return "allow"
	case PermissionPrompt:
		return "prompt"
	case PermissionDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// EvaluateTrust determines whether a tool call should be allowed, prompted, or denied
// based on the configured trust tier, the tool name, and the command.
func EvaluateTrust(tier, toolName, command string) PermissionDecision {
	switch tier {
	case "assist":
		return evaluateAssist(toolName, command)
	case "auto":
		return evaluateAuto(toolName, command)
	default: // "suggest" or unknown
		return PermissionDeny
	}
}

func evaluateAssist(toolName, command string) PermissionDecision {
	switch toolName {
	case "Read", "Glob", "Grep", "Search":
		return PermissionAllow
	case "Write", "Edit":
		return PermissionPrompt
	case "Bash":
		risk := ClassifyCommand(command)
		switch risk {
		case CommandRiskReadOnly, CommandRiskTest:
			return PermissionAllow
		case CommandRiskDestructive:
			return PermissionDeny
		default:
			return PermissionPrompt
		}
	default:
		return PermissionPrompt
	}
}

func evaluateAuto(toolName, command string) PermissionDecision {
	switch toolName {
	case "Read", "Glob", "Grep", "Search":
		return PermissionAllow
	case "Write", "Edit":
		return PermissionPrompt
	case "Bash":
		risk := ClassifyCommand(command)
		switch risk {
		case CommandRiskReadOnly, CommandRiskTest, CommandRiskGeneral:
			return PermissionAllow
		default: // destructive
			return PermissionPrompt
		}
	default:
		return PermissionPrompt
	}
}
