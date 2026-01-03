package history

import (
	"testing"
)

func TestParseSudoCommand_Simple(t *testing.T) {
	raw := "sudo apt-get update"
	result := ParseSudoCommand(raw)

	if !result.IsSudo {
		t.Error("IsSudo should be true")
	}
	if result.Command != "apt-get update" {
		t.Errorf("Command = %q, want %q", result.Command, "apt-get update")
	}
	if result.SudoUser != "root" {
		t.Errorf("SudoUser = %q, want %q", result.SudoUser, "root")
	}
}

func TestParseSudoCommand_WithUser(t *testing.T) {
	raw := "sudo -u postgres psql"
	result := ParseSudoCommand(raw)

	if !result.IsSudo {
		t.Error("IsSudo should be true")
	}
	if result.Command != "psql" {
		t.Errorf("Command = %q, want %q", result.Command, "psql")
	}
	if result.SudoUser != "postgres" {
		t.Errorf("SudoUser = %q, want %q", result.SudoUser, "postgres")
	}
}

func TestParseSudoCommand_NotSudo(t *testing.T) {
	raw := "echo hello"
	result := ParseSudoCommand(raw)

	if result.IsSudo {
		t.Error("IsSudo should be false")
	}
	if result.Command != "echo hello" {
		t.Errorf("Command = %q, want %q", result.Command, "echo hello")
	}
}

func TestParseSudoCommand_DoasSudo(t *testing.T) {
	raw := "doas apt-get update"
	result := ParseSudoCommand(raw)

	if !result.IsSudo {
		t.Error("IsSudo should be true for doas")
	}
	if result.Command != "apt-get update" {
		t.Errorf("Command = %q, want %q", result.Command, "apt-get update")
	}
}

func TestParseSudoCommand_SudoWithEnv(t *testing.T) {
	raw := "sudo -E npm install"
	result := ParseSudoCommand(raw)

	if !result.IsSudo {
		t.Error("IsSudo should be true")
	}
	if result.Command != "npm install" {
		t.Errorf("Command = %q, want %q", result.Command, "npm install")
	}
}
