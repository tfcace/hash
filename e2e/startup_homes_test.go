//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// startupBound is how long a home's startup may take to reach the prompt.
// Each takes well under a second; a hang is what this catches.
const startupBound = 5 * time.Second

// Debian/Ubuntu's skeleton .profile and .bashrc with nvm (a default alias),
// pyenv, conda and rustup's lines. It guards: nvm's flag tests on $-
// (`[ "${-#*e}" != "$-" ]`) read every flag as set, so nvm() recursed
// forever and the interactive shell hung at startup; conda's and pyenv's
// init, whose commands are missing and whose errors go to /dev/null,
// printed "command not found" banners anyway.
func TestStartupHome_DebianBashWithNvm(t *testing.T) {
	h := newFixtureHome(t, "debian")
	h.mkdir("~/bin", "~/.local/bin", "~/.pyenv/bin", "~/.cargo/bin")
	h.migrated("bash", "~/.profile", "~/.bashrc")

	ht := startHash(t, h, termSize{})
	ht.waitPrompts(1, startupBound)
	if out := ht.startupOutput(); strings.TrimSpace(out) != "" {
		t.Errorf("startup printed %q, want nothing", h.unhome(out))
	}
	if got := strings.TrimSpace(ht.command("nvm current", 5*time.Second)); got != "v22.1.0" {
		t.Errorf("nvm current printed %q, want v22.1.0 (the default alias)", got)
	}
	if got := strings.TrimSpace(ht.command(`echo "flags=$-"`, 5*time.Second)); !strings.Contains(got, "i") {
		t.Errorf("$- at the prompt is %q, want the interactive flag i", got)
	}
	if got := strings.TrimSpace(ht.command("alias gs", 5*time.Second)); !strings.Contains(got, "git status") {
		t.Errorf("alias gs printed %q, want ~/.bash_aliases' alias", got)
	}
	ht.exit()
}

// hash -ilc reads the migrated files a login interactive bash reads, so
// `$SHELL -ilc env` (how editors and tools read a user's environment) sees
// the PATH they set, ahead of the system's; hash -ic has i in $-, so the
// Debian .bashrc gets past its "not interactive" guard.
func TestStartupHome_DebianScriptModesSeeTheUsersPath(t *testing.T) {
	h := newFixtureHome(t, "debian")
	h.mkdir("~/bin", "~/.local/bin", "~/.pyenv/bin", "~/.cargo/bin")
	h.migrated("bash", "~/.profile", "~/.bashrc")

	stdout, stderr, err := h.run("-ilc", "env")
	if err != nil || stderr != "" {
		t.Fatalf("hash -ilc env: %v, stderr %q", err, h.unhome(stderr))
	}
	var path string
	for _, line := range strings.Split(stdout, "\n") {
		if p, ok := strings.CutPrefix(line, "PATH="); ok {
			path = h.unhome(p)
		}
	}
	entries := strings.Split(path, ":")
	var home []string
	for i, e := range entries {
		if !strings.HasPrefix(e, "~/") {
			if i < 6 {
				t.Fatalf("PATH %q: system entry %q before the user's own", path, e)
			}
			continue
		}
		home = append(home, e)
	}
	// .bashrc's entries, last prepended first, then .profile's. hash runs
	// .bashrc after .profile, where bash runs it inside .profile (Debian's
	// .profile sources it only for $BASH_VERSION), so the two files' blocks
	// are the other way round from bash's (the backlog's by-design item).
	want := []string{
		"~/.cargo/bin", "~/miniconda3/bin", "~/.pyenv/bin", "~/.nvm/versions/node/v22.1.0/bin",
		"~/.local/bin", "~/bin",
	}
	if strings.Join(home, ":") != strings.Join(want, ":") {
		t.Errorf("hash -ilc env: the home's PATH entries are\n %v\nwant\n %v", home, want)
	}

	stdout, stderr, err = h.run("-ic", `echo "flags=$-"; type ll >/dev/null && echo ll-defined`)
	if err != nil || stderr != "" || !strings.Contains(stdout, "ll-defined") {
		t.Errorf("hash -ic: %v, stdout %q stderr %q; want $- with i and .bashrc's ll alias", err, stdout, h.unhome(stderr))
	}
	if flags, _, _ := strings.Cut(strings.TrimPrefix(stdout, "flags="), "\n"); !strings.Contains(flags, "i") {
		t.Errorf("hash -ic: $- is %q, want i in it", flags)
	}
}

// The other homes from the startup review, each with what it guards. A home
// reaches the prompt within startupBound and prints only the lines listed:
// one "not loaded" line per file hash cannot load (notLoaded, each exactly
// once), and lines matching also. Then check runs at the prompt and must
// print want.
func TestStartupHome_RealWorldLayouts(t *testing.T) {
	for _, tt := range []struct {
		name, why   string
		shell, from string
		files       []string
		notLoaded   []string
		also        string
		check, want string
	}{
		{
			name: "fedora",
			why: "Fedora's .bash_profile and .bashrc: /etc/bashrc's constructs, a quoted =~ PATH test, .bashrc.d, " +
				"rbenv's init (missing, 2>/dev/null: no banner), asdf",
			shell: "/bin/bash", from: "bash", files: []string{"~/.bash_profile", "~/.bashrc"},
			check: `echo "$GLOBAL_BASHRC_RAN $FROM_D $BASH_PROFILE_RAN"; alias k; asdf current`,
			want:  "1 1 1\nalias k='kubectl'\nasdf current\n",
		},
		{
			name: "ohmyzsh",
			why: "zsh with oh-my-zsh: its non-zsh guard printed a red error and the process tree at every start; " +
				"now the file loads nothing and says so once, and .zshrc goes on",
			shell: "/bin/zsh", from: "zsh", files: []string{"~/.zshenv", "~/.zprofile", "~/.zshrc"},
			notLoaded: []string{"hash: ~/.oh-my-zsh/oh-my-zsh.sh: not loaded"},
			check:     `echo "$ZSHENV_RAN $ZPROFILE_RAN $ZSHRC_RAN omz=$OMZ_LOADED"; alias zshconfig`,
			want:      "1 1 1 omz=\nalias zshconfig='mate ~/.zshrc'\n",
		},
		{
			name:  "prezto",
			why:   "prezto's init.zsh stops at a zsh-only for; hash sourced it silently",
			shell: "/bin/zsh", from: "zsh", files: []string{"~/.zshrc"},
			notLoaded: []string{"hash: ~/.zprezto/init.zsh: not loaded"},
			check:     `echo "$PREZRC_RAN"; pz`,
			want:      "1\nprezto-alias\n",
		},
		{
			name:  "zinit",
			why:   "zinit.zsh does not load (zsh's brace if); hash sourced it silently, leaving only the banners",
			shell: "/bin/zsh", from: "zsh", files: []string{"~/.zshrc"},
			notLoaded: []string{"hash: ~/.local/share/zinit/zinit.git/zinit.zsh: not loaded"},
			// The consequences of zinit not loading (the backlog: they print
			// before the not-loaded line).
			also:  `✗ zinit: command not found`,
			check: `echo "$ZINITRC_RAN"; zz`,
			want:  "1\nzinit-alias\n",
		},
		{
			name: "execzsh",
			why: "exec zsh in a .bashrc that the migrated .bash_profile sources replaced hash; the sourced " +
				".bashrc is filtered as bash's, so the exec is skipped",
			shell: "/bin/bash", from: "bash", files: []string{"~/.bash_profile", "~/.bashrc"},
			check: `echo "$RC $RC2 $BP $HASH_SHELL"`,
			want:  "1 1 1 1\n",
		},
		{
			name: "broken",
			why: "a .bashrc that does not parse, sourced by the migrated .bash_profile: reported once (it was " +
				"listed twice), its statements before the error run, the rest of .bash_profile runs",
			shell: "/bin/bash", from: "bash", files: []string{"~/.bash_profile", "~/.bashrc"},
			notLoaded: []string{"hash: ~/.bashrc: not loaded"},
			check:     `echo "b=$BEFORE a=$AFTER p=$PROFILE_AFTER"; before`,
			want:      "b=1 a= p=1\nbefore\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Log(tt.why)
			h := newFixtureHome(t, tt.name)
			h.shell = tt.shell
			h.migrated(tt.from, tt.files...)

			ht := startHash(t, h, termSize{})
			ht.waitPrompts(1, startupBound)
			startup := h.unhome(ht.startupOutput())
			counts := map[string]int{}
			for _, line := range strings.Split(startup, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				matched := tt.also != "" && line == tt.also
				for _, want := range tt.notLoaded {
					if strings.HasPrefix(line, want) {
						counts[want]++
						matched = true
					}
				}
				if !matched {
					t.Errorf("startup printed an unexpected line %q", line)
				}
			}
			for _, want := range tt.notLoaded {
				if counts[want] != 1 {
					t.Errorf("startup printed %q %d times, want once:\n%s", want, counts[want], startup)
				}
			}
			if strings.Count(startup, "not loaded") != len(tt.notLoaded) {
				t.Errorf("startup has %d not-loaded lines, want %d:\n%s", strings.Count(startup, "not loaded"), len(tt.notLoaded), startup)
			}
			if got := h.unhome(ht.command(tt.check, 5*time.Second)); got != tt.want {
				t.Errorf("%s printed %q, want %q", tt.check, got, tt.want)
			}
			ht.exit()
		})
	}
}

// Ctrl+C during a startup file's endless loop ends that file and reaches
// the prompt with $? 130, as bash does. Startup steps did not claim SIGINT,
// so nothing could stop the loop; then the prompt came with $? 1.
func TestStartupHome_CtrlCEndsAStartupLoop(t *testing.T) {
	h := newTestHome(t)
	h.write("~/.hashrc", "echo loop-started\nwhile :; do :; done\necho after-loop\n")

	ht := startHash(t, h, termSize{})
	ht.waitFor("loop-started", startupBound)
	time.Sleep(100 * time.Millisecond)
	ht.typeText("\x03")
	ht.waitPrompts(1, startupBound)
	if got := ht.command(`echo "st=$?"`, 5*time.Second); got != "st=130\n" {
		t.Errorf("first line after Ctrl+C printed %q, want st=130", got)
	}
	if strings.Contains(ht.plain(), "after-loop") {
		t.Errorf("the rest of the interrupted .hashrc ran:\n%s", ht.plain())
	}
	ht.exit()
}
