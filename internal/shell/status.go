package shell

import (
	"fmt"
	"strings"
)

// SystemStatus holds the current status of all shell subsystems.
type SystemStatus struct {
	Version string

	// Config
	ConfigPath string
	ConfigOK   bool
	ConfigErr  string

	// Prompt
	PromptMode string
	PromptOK   bool
	PromptErr  string

	// History
	HistoryPath  string
	HistoryOK    bool
	HistoryErr   string
	HistoryCount int64

	// Learning
	LearningOK   bool
	LearningErr  string
	PatternCount int64

	// Agent
	AgentName string
	AgentOK   bool
	AgentErr  string

	// PTY
	PTYOK  bool
	PTYErr string

	// Clipboard
	ClipboardOK  bool
	ClipboardErr string
}

// Format returns a formatted status display.
func (s *SystemStatus) Format() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Shell:     hash %s\n", s.Version)

	// Config
	if s.ConfigOK {
		fmt.Fprintf(&b, "Config:    %s \033[32m✓\033[0m\n", s.ConfigPath)
	} else {
		fmt.Fprintf(&b, "Config:    \033[31m✗\033[0m %s\n", s.ConfigErr)
	}

	// Prompt
	if s.PromptOK {
		fmt.Fprintf(&b, "Prompt:    %s \033[32m✓\033[0m\n", s.PromptMode)
	} else {
		fmt.Fprintf(&b, "Prompt:    %s \033[31m✗\033[0m %s\n", s.PromptMode, s.PromptErr)
	}

	// History
	if s.HistoryOK {
		fmt.Fprintf(&b, "History:   %s \033[32m✓\033[0m (%s entries)\n",
			s.HistoryPath, formatNumber(s.HistoryCount))
	} else {
		fmt.Fprintf(&b, "History:   \033[31m✗\033[0m %s\n", s.HistoryErr)
	}

	// Learning
	if s.LearningOK {
		fmt.Fprintf(&b, "Learning:  enabled \033[32m✓\033[0m (%d patterns)\n", s.PatternCount)
	} else {
		fmt.Fprintf(&b, "Learning:  \033[31m✗\033[0m %s\n", s.LearningErr)
	}

	// Agent
	if s.AgentOK {
		fmt.Fprintf(&b, "Agent:     %s \033[32m✓\033[0m\n", s.AgentName)
	} else {
		fmt.Fprintf(&b, "Agent:     %s (not connected)\n", s.AgentName)
	}

	// PTY
	if s.PTYOK {
		b.WriteString("PTY:       available \033[32m✓\033[0m\n")
	} else {
		fmt.Fprintf(&b, "PTY:       \033[33m⚠\033[0m %s\n", s.PTYErr)
	}

	// Clipboard
	if s.ClipboardOK {
		b.WriteString("Clipboard: available \033[32m✓\033[0m\n")
	} else {
		fmt.Fprintf(&b, "Clipboard: \033[33m⚠\033[0m %s\n", s.ClipboardErr)
	}

	return b.String()
}

func formatNumber(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", n/1000000, (n%1000000)/1000, n%1000)
}
