package shell

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/config"
)

// startupHome writes files into an isolated HOME (with no migration) and
// returns a shell of mode whose login and interactive startup files are
// the named ones in it.
func startupHome(t *testing.T, mode Mode, files map[string]string, login, interactive []string) (*Shell, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", "/bin/sh")
	clearTestExports(t)
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Shell.InitCommands = nil
	cfg.Shell.StartupFiles.Login, cfg.Shell.StartupFiles.Interactive = nil, nil
	for _, name := range login {
		cfg.Shell.StartupFiles.Login = append(cfg.Shell.StartupFiles.Login, filepath.Join(home, name))
	}
	for _, name := range interactive {
		cfg.Shell.StartupFiles.Interactive = append(cfg.Shell.StartupFiles.Interactive, filepath.Join(home, name))
	}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sh.Close() })
	return sh, home
}

// exit and exec in a startup file end the shell, as in bash: the
// interactive shell ran the rest of the file and showed its prompt.
func TestStartup_ExitOrExecInAStartupFileEndsTheShell(t *testing.T) {
	for _, tt := range []struct {
		name, rc string
		status   int
	}{
		{"exit", "export HASH_T_BEFORE=1\nexit 3\nexport HASH_T_AFTER=1\n", 3},
		{"exec", "export HASH_T_BEFORE=1\nexec sh -c 'exit 4'\nexport HASH_T_AFTER=1\n", 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sh, _ := startupHome(t, Mode{Interactive: true}, map[string]string{".hashrc": tt.rc, ".rc2": "export HASH_T_NEXT=1\n"},
				nil, []string{".hashrc", ".rc2"})
			err := sh.runStartup(context.Background())
			var exit shellExit
			if !errors.As(err, &exit) || exit.status != tt.status {
				t.Fatalf("runStartup = %v, want the shell's exit with %d", err, tt.status)
			}
			if shellVar(t, sh, "HASH_T_BEFORE") != "1" || shellVar(t, sh, "HASH_T_AFTER") != "" || shellVar(t, sh, "HASH_T_NEXT") != "" {
				t.Errorf("before=%q after=%q next=%q, want only the lines before the %s run", shellVar(t, sh, "HASH_T_BEFORE"),
					shellVar(t, sh, "HASH_T_AFTER"), shellVar(t, sh, "HASH_T_NEXT"), tt.name)
			}
		})
	}
}

// An expansion error ends the file it is in; the interactive shell goes on
// to the next file.
func TestStartup_AnExpansionErrorEndsOnlyItsFile(t *testing.T) {
	sh, _ := startupHome(t, Mode{Interactive: true}, map[string]string{
		".hashrc": "export HASH_T_BEFORE=1\n: ${HASH_T_UNSET:?unset}\nexport HASH_T_AFTER=1\n",
		".rc2":    "export HASH_T_NEXT=1\n",
	}, nil, []string{".hashrc", ".rc2"})
	if err := sh.runStartup(context.Background()); err != nil {
		t.Fatalf("runStartup = %v, want startup to go on", err)
	}
	if shellVar(t, sh, "HASH_T_BEFORE") != "1" || shellVar(t, sh, "HASH_T_AFTER") != "" || shellVar(t, sh, "HASH_T_NEXT") != "1" {
		t.Errorf("before=%q after=%q next=%q, want the file to stop and the next to run", shellVar(t, sh, "HASH_T_BEFORE"),
			shellVar(t, sh, "HASH_T_AFTER"), shellVar(t, sh, "HASH_T_NEXT"))
	}
}

// Ctrl+C that ends the last startup step leaves $? at 130 for the first
// prompt, as bash's, and as a line typed at the prompt that Ctrl+C ended:
// it was 1, the interpreter's status for a context it saw end.
func TestStartup_CtrlCLeavesTheStatus130(t *testing.T) {
	sh, _ := startupHome(t, Mode{Interactive: true}, map[string]string{".hashrc": "while :; do :; done\n"}, nil, []string{".hashrc"})
	sh.config.Shell.RCCommands = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sh.runStartup(ctx) }()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runStartup = %v, want startup to go on", err)
			}
			if got := shellVar(t, sh, "?"); got != "130" {
				t.Errorf("$? = %s after Ctrl+C ended the startup, want 130", got)
			}
			return
		case <-tick.C:
			sh.interrupts.fire()
		case <-deadline:
			cancel()
			<-done
			t.Fatal("Ctrl+C did not end the startup step's loop")
		}
	}
}

// recordImport records an imported migration of the files names in home,
// written for shell.
func recordImport(t *testing.T, home, shell string, names ...string) {
	t.Helper()
	var paths []string
	for _, name := range names {
		paths = append(paths, filepath.Join(home, name))
	}
	files, report, errs := compat.FilterFiles(paths, shell, "bash")
	if len(errs) != 0 || report == nil {
		t.Fatalf("FilterFiles: %v", errs)
	}
	if _, _, err := compat.RecordImport(compat.DefaultStatePath(), shell, files, report); err != nil {
		t.Fatal(err)
	}
}

// A login shell runs the system and login files first, then the migrated
// files, then ~/.hashrc, as bash and zsh run /etc/profile before the
// user's: the migrated files ran first, and macOS's path_helper in
// /etc/profile put the system's directories before the user's PATH.
func TestStartup_LoginFilesRunBeforeTheMigratedOnes(t *testing.T) {
	step := func(name string) string { return "export HASH_T_ORDER=\"${HASH_T_ORDER}" + name + " \"\n" }
	sh, home := startupHome(t, Mode{Login: true, Interactive: true}, map[string]string{
		"etc-profile": step("login"),
		".zprofile":   step("zprofile"),
		".zshrc":      step("zshrc"),
		".hashrc":     step("hashrc"),
	}, []string{"etc-profile"}, []string{".hashrc"})
	t.Setenv("HASH_T_ORDER", "")
	recordImport(t, home, "zsh", ".zprofile", ".zshrc")
	if err := sh.runStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := shellVar(t, sh, "HASH_T_ORDER"); got != "login zprofile zshrc hashrc" {
		t.Errorf("startup order %q, want login zprofile zshrc hashrc", got)
	}
}

// A startup file runs once in a startup: a login file the migration
// sources too (~/.profile), and a migrated file another one sources (the
// .bashrc a .bash_profile sources).
func TestStartup_AStartupFileRunsOnce(t *testing.T) {
	count := func(c string) string { return "export HASH_T_RUNS=\"${HASH_T_RUNS}" + c + "\"\n" }
	for _, tt := range []struct {
		name     string
		mode     Mode
		files    map[string]string
		login    []string
		migrated []string
	}{
		{"a login file migrated", Mode{Login: true, Interactive: true},
			map[string]string{".profile": count("p"), ".bashrc": count("r")}, []string{".profile"}, []string{".profile", ".bashrc"}},
		{"a migrated file sourced", Mode{Interactive: true},
			map[string]string{".bash_profile": count("p") + ". ~/.bashrc\n", ".bashrc": count("r")}, nil, []string{".bash_profile", ".bashrc"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sh, home := startupHome(t, tt.mode, tt.files, tt.login, nil)
			t.Setenv("HASH_T_RUNS", "")
			recordImport(t, home, "bash", tt.migrated...)
			if err := sh.runStartup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := shellVar(t, sh, "HASH_T_RUNS"); got != "pr" {
				t.Errorf("runs %q, want pr: each file once", got)
			}
		})
	}
}

// A startup file in which a statement does not parse says it did not load,
// one line for the file: it was silent.
func TestStartup_AFileThatDoesNotLoadSaysSo(t *testing.T) {
	sh, _ := startupHome(t, Mode{Interactive: true}, map[string]string{".hashrc": "export HASH_T_A=1\n}\n"}, nil, []string{".hashrc"})
	st := sh.startup()
	var errOut bytes.Buffer
	st.stderr = &errOut
	st.Run(context.Background(), func(context.Context) StepOutcome { return StepOutcome{} })
	if got := errOut.String(); !strings.HasPrefix(got, "hash: ~/.hashrc: not loaded: line 2: ") || strings.Count(got, "\n") != 1 {
		t.Errorf("stderr %q, want one line saying ~/.hashrc did not load", got)
	}
}

// A migrated file edited into one that does not load says so at each
// startup, and the change notice does not count it a skipped item: it said
// "1 item skipped" of a .bashrc nothing in ran.
func TestStartup_AMigratedFileThatDoesNotLoadSaysSo(t *testing.T) {
	sh, home := startupHome(t, Mode{Interactive: true}, map[string]string{".bashrc": "alias k=kubectl\n"}, nil, nil)
	recordImport(t, home, "bash", ".bashrc")
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("alias k=kubectl\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(bashrc, future, future); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st := sh.startup()
		var out, errOut bytes.Buffer
		st.stdout, st.stderr = &out, &errOut
		sh.runMigration(context.Background(), st, compat.LineReader(strings.NewReader("")), &out)
		if got := errOut.String(); !strings.HasPrefix(got, "hash: ~/.bashrc: not loaded: line 2: ") {
			t.Errorf("stderr %q, want ~/.bashrc not loaded", got)
		}
		if strings.Contains(out.String(), "1 item skipped") {
			t.Errorf("notice %q counts the file not loaded a skipped item", out.String())
		}
	}
}

// A file a startup file sources that does not parse says so, one line for
// the file, and none of it runs: oh-my-zsh.sh, bash code to the bash
// dialect that is zsh's, loaded nothing and said nothing, then ran its
// guard (an error and the process tree at each start). The import summary
// and the migration state (for hash migrate status) name it too.
func TestStartup_ASourcedFileThatDoesNotLoadSaysSo(t *testing.T) {
	omz := "echo omz-guard >&2\n[[ -z \"$ZSH\" ]] && export ZSH=\"${${(%):-%x}:a:h}\"\n"
	sh, home := startupHome(t, Mode{Interactive: true}, map[string]string{
		"rc":     "source ~/omz.sh\n",
		"omz.sh": omz,
		".zshrc": "source ~/omz.sh\n",
	}, nil, []string{"rc"})
	st := sh.startup()
	var out, errOut bytes.Buffer
	st.stdout, st.stderr = &out, &errOut
	st.Run(context.Background(), func(context.Context) StepOutcome { return StepOutcome{} })
	const want = "hash: ~/omz.sh: not loaded: line 2: "
	if got := errOut.String(); !strings.HasPrefix(got, want) || strings.Count(got, "\n") != 1 {
		t.Errorf("stderr %q, want one line %q...", got, want)
	}

	t.Setenv("SHELL", "/bin/zsh")
	out.Reset()
	errOut.Reset()
	st = sh.startup()
	st.stdout, st.stderr = &out, &errOut
	sh.runMigration(context.Background(), st, compat.LineReader(strings.NewReader("y\n")), &out)
	if !strings.Contains(out.String(), "~/omz.sh: not loaded: line 2: ") || strings.Contains(errOut.String(), "omz-guard") {
		t.Errorf("import summary %q, want it to name ~/omz.sh", out.String())
	}
	state, err := compat.LoadState(compat.DefaultStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if n := state.Import.SourcedNotLoaded; len(n) != 1 || n[0].File != filepath.Join(home, "omz.sh") || n[0].Line != 2 {
		t.Errorf("state's sourced files not loaded %+v, want ~/omz.sh", n)
	}
}

// clearTestExports unsets the HASH_T_ variables the tests' startup files
// export, now and once t ends: an executor's exports are the process's own
// (it is the one shell of hash's process), so one test's would reach the
// next.
func clearTestExports(t *testing.T) {
	t.Helper()
	unset := func() {
		for _, kv := range os.Environ() {
			if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "HASH_T_") {
				_ = os.Unsetenv(name)
			}
		}
	}
	unset()
	t.Cleanup(unset)
}

// Ctrl+C ends a startup step's in-process work (a loop of builtins in a
// startup file or a command of the config), as it ends a line's, and the
// next step runs: nothing claimed the signal, so a loop in ~/.hashrc held
// the shell before its first prompt for good.
func TestStartup_CtrlCEndsAStepAndTheNextRuns(t *testing.T) {
	const loop = "export HASH_T_BEFORE=1\nwhile :; do :; done\nexport HASH_T_AFTER=1\n"
	for _, tt := range []struct {
		name  string
		files map[string]string
		rc    []string
	}{
		{"a file", map[string]string{".hashrc": loop}, nil},
		{"a command", map[string]string{".hashrc": "export HASH_T_BEFORE=1\n"}, []string{"while :; do :; done; export HASH_T_AFTER=1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sh, _ := startupHome(t, Mode{Interactive: true}, tt.files, nil, []string{".hashrc"})
			sh.config.Shell.RCCommands = append(tt.rc, "export HASH_T_NEXT=1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- sh.runStartup(ctx) }()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			deadline := time.After(10 * time.Second)
			for {
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("runStartup = %v, want startup to go on", err)
					}
					if shellVar(t, sh, "HASH_T_BEFORE") != "1" || shellVar(t, sh, "HASH_T_AFTER") != "" || shellVar(t, sh, "HASH_T_NEXT") != "1" {
						t.Errorf("before=%q after=%q next=%q, want the step to stop and the next to run", shellVar(t, sh, "HASH_T_BEFORE"),
							shellVar(t, sh, "HASH_T_AFTER"), shellVar(t, sh, "HASH_T_NEXT"))
					}
					return
				case <-tick.C:
					sh.interrupts.fire()
				case <-deadline:
					cancel() // the shell ending ends it
					<-done
					t.Fatal("Ctrl+C did not end the startup step's loop")
				}
			}
		})
	}
}

// A migrated file that does not load, which another migrated file sources
// (the Fedora .bash_profile sources .bashrc), is listed once as not loaded:
// the filter's word on it and the run's were two entries of the import
// summary, the state and hash migrate status.
func TestStartup_AFileThatDoesNotLoadIsListedOnce(t *testing.T) {
	sh, home, statePath := migrationShell(t, "/bin/bash", map[string]string{
		".bash_profile": ". ~/.bashrc\n",
		".bashrc":       "alias k=kubectl\n}\n",
	})
	st := sh.startup()
	var out, errOut bytes.Buffer
	st.stdout, st.stderr = &out, &errOut
	sh.runMigration(context.Background(), st, compat.LineReader(strings.NewReader("y\n")), &out)
	if n := strings.Count(out.String(), "~/.bashrc: not loaded"); n != 1 {
		t.Errorf("import summary names ~/.bashrc not loaded %d times, want once:\n%s", n, out.String())
	}
	if n := strings.Count(errOut.String(), "~/.bashrc: not loaded"); n != 1 {
		t.Errorf("startup says ~/.bashrc did not load %d times, want once: %q", n, errOut.String())
	}
	state, err := compat.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range state.Import.SourcedNotLoaded {
		if n.File == filepath.Join(home, ".bashrc") {
			t.Errorf("state lists the migrated ~/.bashrc as a sourced file not loaded: %+v", n)
		}
	}
}

// A file a migrated file sources that is written for the migrated shell
// (the .bashrc a Fedora .bash_profile sources) is filtered as that
// shell's, as the migrated files are: its exec zsh is skipped. It ran as it
// is, and exec zsh replaced hash at each start, in the interactive shell
// and in hash -ilc.
func TestStartup_AFileAMigratedFileSourcesIsFilteredAsItsShells(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(sh *Shell) StepOutcome
	}{
		{"interactive", func(sh *Shell) StepOutcome {
			if err := sh.runStartup(context.Background()); err != nil {
				return StepOutcome{End: StepExited}
			}
			return StepOutcome{}
		}},
		{"hash -ilc", func(sh *Shell) StepOutcome {
			st := NewStartup(sh.executor, sh.config, true, true)
			return st.Run(context.Background(), st.Migrated)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sh, home := startupHome(t, Mode{Interactive: true}, map[string]string{
				".bash_profile": "export HASH_T_P=1\n. ~/.bashrc\n",
				".bashrc":       "export HASH_T_A=1\nexec zsh\nexport HASH_T_B=1\n",
			}, nil, nil)
			bin := filepath.Join(home, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "zsh"), []byte("#!/bin/sh\nexit 5\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			recordImport(t, home, "bash", ".bash_profile")
			if out := tt.run(sh); out.End != StepDone {
				t.Fatalf("startup ended %+v: exec zsh in the sourced .bashrc ran", out)
			}
			if shellVar(t, sh, "HASH_T_P") != "1" || shellVar(t, sh, "HASH_T_A") != "1" || shellVar(t, sh, "HASH_T_B") != "1" {
				t.Errorf("p=%q a=%q b=%q, want all of .bash_profile and .bashrc but the exec to run", shellVar(t, sh, "HASH_T_P"),
					shellVar(t, sh, "HASH_T_A"), shellVar(t, sh, "HASH_T_B"))
			}
		})
	}
}
