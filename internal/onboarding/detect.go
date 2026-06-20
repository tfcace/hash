// Package onboarding provides the first-run, agent-aware welcome experience:
// detecting installed ACP adapters, writing an initial agent config, and the
// Bubble Tea panel that ties setup and orientation together.
package onboarding

// Agent describes a supported ACP adapter Hash can drive.
type Agent struct {
	Name    string   // display label, e.g. "Claude"
	Command string   // executable to look up on PATH
	Args    []string // extra args needed to speak ACP (e.g. gemini --experimental-acp)
	Desc    string   // one-line description for the picker
}

// Known lists the adapters the onboarding flow can set up, in display order.
var Known = []Agent{
	{Name: "Claude", Command: "claude-agent-acp", Desc: "Claude Code over ACP"},
	{Name: "Gemini", Command: "gemini", Args: []string{"--experimental-acp"}, Desc: "Gemini CLI"},
}

// Detect returns the subset of Known adapters found on PATH. lookPath is
// injected (exec.LookPath in production) so the probe is testable.
func Detect(lookPath func(string) (string, error)) []Agent {
	var found []Agent
	for _, a := range Known {
		if _, err := lookPath(a.Command); err == nil {
			found = append(found, a)
		}
	}
	return found
}
