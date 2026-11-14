// internal/compat/detect_test.go
package compat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectShell_FromEnv(t *testing.T) {
	tests := []struct {
		shellEnv string
		want     string
	}{
		{"/bin/zsh", "zsh"},
		{"/usr/bin/zsh", "zsh"},
		{"/bin/bash", "bash"},
		{"/usr/bin/bash", "bash"},
		{"/usr/local/bin/zsh", "zsh"},
		{"zsh", "zsh"},
		{"bash", "bash"},
		{"/bin/sh", "sh"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.shellEnv, func(t *testing.T) {
			got := shellFromEnv(tt.shellEnv)
			if got != tt.want {
				t.Errorf("shellFromEnv(%q) = %q, want %q", tt.shellEnv, got, tt.want)
			}
		})
	}
}

func TestDetectRCFile(t *testing.T) {
	tmpDir := t.TempDir()
	home := tmpDir

	// Create test rc files
	zshrc := filepath.Join(home, ".zshrc")
	os.WriteFile(zshrc, []byte("# zsh"), 0o644) //nolint:gosec // G306: test file

	bashrc := filepath.Join(home, ".bashrc")
	os.WriteFile(bashrc, []byte("# bash"), 0o644) //nolint:gosec // G306: test file

	tests := []struct {
		shell string
		want  string
	}{
		{"zsh", zshrc},
		{"bash", bashrc},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			got := detectRCFile(tt.shell, home)
			if got != tt.want {
				t.Errorf("detectRCFile(%q) = %q, want %q", tt.shell, got, tt.want)
			}
		})
	}
}

func TestDetectProfileFile(t *testing.T) {
	tmpDir := t.TempDir()
	home := tmpDir

	// Create test profile files
	zprofile := filepath.Join(home, ".zprofile")
	os.WriteFile(zprofile, []byte("# zprofile"), 0o644) //nolint:gosec // G306: test file

	tests := []struct {
		shell string
		want  string
	}{
		{"zsh", zprofile},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			got := detectProfileFile(tt.shell, home)
			if got != tt.want {
				t.Errorf("detectProfileFile(%q) = %q, want %q", tt.shell, got, tt.want)
			}
		})
	}
}

func TestShellFiles_Files(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	zprofile := filepath.Join(tmpDir, ".zprofile")
	os.WriteFile(zprofile, []byte("# profile"), 0644)

	zshrc := filepath.Join(tmpDir, ".zshrc")
	os.WriteFile(zshrc, []byte("# rc"), 0644)

	sf := ShellFiles{
		Shell:       "zsh",
		ProfileFile: zprofile,
		RCFile:      zshrc,
	}

	files := sf.Files()
	if len(files) != 2 {
		t.Errorf("Files() returned %d files, want 2", len(files))
	}
	// Profile should come before RC
	if files[0] != zprofile {
		t.Errorf("Files()[0] = %q, want profile first", files[0])
	}
	if files[1] != zshrc {
		t.Errorf("Files()[1] = %q, want rc second", files[1])
	}
}

func TestShellForPath(t *testing.T) {
	for path, want := range map[string]string{
		"/h/.zshrc": "zsh", "/h/.zprofile": "zsh", "/x/plugin.zsh": "zsh",
		"/h/.bashrc": "bash", "/h/.profile": "bash", "/x/a.sh": "bash",
		"/h/.hashrc": "", "/etc/profile": "",
		// zsh's files by their other names
		"/etc/zshrc": "zsh", "/etc/zprofile": "zsh", "/etc/zshenv": "zsh", "/etc/zlogin": "zsh",
		"/etc/zsh/zshrc": "zsh", "/h/.zshrc.local": "zsh", "/h/.zsh_aliases": "zsh", "/h/.zlogout": "zsh",
		"/h/.zshenv.local": "zsh",
		// the extension wins over the name
		"/x/zsh-compat.sh": "bash", "/h/.zshrc.bash": "bash", "/x/zprofile.sh": "bash", "/h/.bashrc.zsh": "zsh",
	} {
		if got := ShellForPath(path); got != want {
			t.Errorf("ShellForPath(%q) = %q, want %q", path, got, want)
		}
	}
}
