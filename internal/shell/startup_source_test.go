package shell

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/config"
)

// A zsh file a startup file sources, or a line sources, is filtered for the
// bash dialect as the startup file is: its aliases and exports load, its
// zsh-only lines are skipped. A bash file is sourced as it is.
func TestStartup_SourcedZshFileIsFiltered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("HASH_TEST_PLUGIN", "")
	files := map[string]string{
		".zshrc": "source ~/plugin.zsh\n",
		"plugin.zsh": "alias pk='echo plugin-alias'\nsetopt extended_glob\nuser=${(%):-%n}\n" +
			"export HASH_TEST_PLUGIN=loaded\n",
		"plain.sh": "alias pb='echo bash-alias'\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&compat.State{Status: compat.StatusDeclined}).Save(compat.DefaultStatePath()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.StartupFiles.Interactive = []string{filepath.Join(home, ".zshrc")}
	cfg.Shell.InitCommands = nil
	sh, err := NewWithMode(cfg, Mode{Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sh.Close() })
	if err := sh.runStartup(context.Background()); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	line := `pk; echo "$HASH_TEST_PLUGIN"; unalias pk; HASH_TEST_PLUGIN=; source ~/plugin.zsh; pk; echo "$HASH_TEST_PLUGIN"; . ~/plain.sh; pb`
	if _, err := sh.executor.Execute(context.Background(), line, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	want := "plugin-alias\nloaded\nplugin-alias\nloaded\nbash-alias\n"
	if out.String() != want || errOut.String() != "" {
		t.Errorf("stdout %q stderr %q, want %q and nothing", out.String(), errOut.String(), want)
	}
}

// A zsh file a startup file sources that the filter cannot load (zinit.zsh,
// prezto's init.zsh) says so, one line for the file, as one that does not
// parse does; the import summary and the migration state name it too. It
// loaded nothing, silently, and its commands were then "not found".
func TestStartup_ASourcedZshFileTheFilterCannotLoadSaysSo(t *testing.T) {
	sh, home := startupHome(t, Mode{Interactive: true}, map[string]string{
		"rc":       "source ~/plug.zsh\n",
		"plug.zsh": "export HASH_T_PLUG=1\n}\n",
		".zshrc":   "source ~/plug.zsh\n",
	}, nil, []string{"rc"})
	st := sh.startup()
	var out, errOut bytes.Buffer
	st.stdout, st.stderr = &out, &errOut
	st.Run(context.Background(), func(context.Context) StepOutcome { return StepOutcome{} })
	const want = "hash: ~/plug.zsh: not loaded: line 2: "
	if got := errOut.String(); !strings.HasPrefix(got, want) || strings.Count(got, "\n") != 1 {
		t.Errorf("stderr %q, want one line %q...", got, want)
	}

	t.Setenv("SHELL", "/bin/zsh")
	out.Reset()
	errOut.Reset()
	st = sh.startup()
	st.stdout, st.stderr = &out, &errOut
	sh.runMigration(context.Background(), st, compat.LineReader(strings.NewReader("y\n")), &out)
	if !strings.Contains(out.String(), "~/plug.zsh: not loaded: line 2: ") {
		t.Errorf("import summary %q, want it to name ~/plug.zsh", out.String())
	}
	state, err := compat.LoadState(compat.DefaultStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if n := state.Import.SourcedNotLoaded; len(n) != 1 || n[0].File != filepath.Join(home, "plug.zsh") || n[0].Line != 2 {
		t.Errorf("state's sourced files not loaded %+v, want ~/plug.zsh", n)
	}
}
