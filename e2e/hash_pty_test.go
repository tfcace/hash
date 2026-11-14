//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The PTY tests drive the real hash binary the way a terminal does: a home
// of their own (HOME, XDG dirs, config), a clean environment, a PTY of a
// fixed size, and a small terminal emulator (vtScreen) that answers hash's
// queries and keeps the screen, so a test can look at what a person sees.

var builtHash struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// TestMain removes the hash binary the PTY tests share.
func TestMain(m *testing.M) {
	code := m.Run()
	if builtHash.dir != "" {
		_ = os.RemoveAll(builtHash.dir)
	}
	os.Exit(code)
}

// hashBinary builds ./cmd/hash once for the test binary's PTY tests.
// HASH_E2E_BIN names a built hash to test instead (an older build, to see a
// test fail where a bug is).
func hashBinary(t testing.TB) string {
	t.Helper()
	if bin := os.Getenv("HASH_E2E_BIN"); bin != "" {
		return bin
	}
	builtHash.once.Do(func() {
		repoRoot, err := filepath.Abs("..")
		if err != nil {
			builtHash.err = err
			return
		}
		builtHash.dir, builtHash.err = os.MkdirTemp("", "hash-e2e-")
		if builtHash.err != nil {
			return
		}
		builtHash.path = filepath.Join(builtHash.dir, "hash")
		build := exec.Command("go", "build", "-o", builtHash.path, "./cmd/hash")
		build.Dir = repoRoot
		if out, err := build.CombinedOutput(); err != nil {
			builtHash.err = fmt.Errorf("build hash: %v\n%s", err, out)
		}
	})
	if builtHash.err != nil {
		t.Fatal(builtHash.err)
	}
	return builtHash.path
}

// testHome is a user's home for one test: $HOME, its XDG dirs, hash's
// config, and the directory hash starts in (work).
type testHome struct {
	t    testing.TB
	dir  string
	work string
	// shell is the user's previous shell ($SHELL), which hash migrates from.
	shell string
}

const testConfig = `[prompt]
mode = "built-in"

[agent]
auto_update = "off"
`

// newTestHome makes an empty home with hash's welcome already shown, the
// built-in prompt (no starship) and no agent update check. The previous
// shell is bash.
func newTestHome(t testing.TB) *testHome {
	t.Helper()
	// The real path (macOS's temp dir is under a symlink), so hash's
	// working directory is under $HOME as it would be.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &testHome{t: t, dir: filepath.Join(tmp, "home"), shell: "/bin/bash"}
	h.work = filepath.Join(h.dir, "work")
	for _, dir := range []string{h.work, h.configDir(), h.dataDir(), filepath.Join(h.dir, ".local", "state"), filepath.Join(h.dir, ".cache")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.write(".config/hash/.welcome_shown", "")
	h.write(".config/hash/config.toml", testConfig)
	return h
}

// newFixtureHome is a home holding testdata/homes/<name>.
func newFixtureHome(t testing.TB, name string) *testHome {
	t.Helper()
	h := newTestHome(t)
	src := filepath.Join("testdata", "homes", name)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		dst := filepath.Join(h.dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *testHome) configDir() string { return filepath.Join(h.dir, ".config", "hash") }
func (h *testHome) dataDir() string   { return filepath.Join(h.dir, ".local", "share", "hash") }

// path is name in the home, "~/" written as the home.
func (h *testHome) path(name string) string {
	return filepath.Join(h.dir, strings.TrimPrefix(name, "~/"))
}

func (h *testHome) write(name, text string) {
	h.t.Helper()
	path := h.path(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *testHome) mkdir(names ...string) {
	h.t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(h.path(name), 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
}

// migrated records that the user imported these startup files of their
// previous shell (hash's first-run question answered yes), so hash sources
// them at every start as it would after the real import.
func (h *testHome) migrated(sourceShell string, files ...string) {
	h.t.Helper()
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = h.path(f)
	}
	state := map[string]any{
		"status":       "imported",
		"source_shell": sourceShell,
		"paths":        paths,
		"import":       map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "summary": map[string]int{}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		h.t.Fatal(err)
	}
	h.write(".local/share/hash/migration.json", string(data))
}

// env is the whole environment hash runs with: nothing of the test's own.
func (h *testHome) env(extra ...string) []string {
	return append([]string{
		"HOME=" + h.dir,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"SHELL=" + h.shell,
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"XDG_CONFIG_HOME=" + filepath.Join(h.dir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(h.dir, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(h.dir, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(h.dir, ".cache"),
	}, extra...)
}

// run runs hash with args and no terminal (hash -ilc ...), from work.
func (h *testHome) run(args ...string) (stdout, stderr string, err error) {
	h.t.Helper()
	cmd := exec.Command(hashBinary(h.t), args...)
	cmd.Dir = h.work
	cmd.Env = h.env()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		h.t.Fatalf("hash %q did not end within 10s; stdout %q stderr %q", args, out.String(), errOut.String())
	}
	return out.String(), errOut.String(), err
}

// unhome writes the home as ~ in text.
func (h *testHome) unhome(text string) string {
	return strings.ReplaceAll(text, h.dir, "~")
}

// hashTerm is a running hash on a PTY.
type hashTerm struct {
	t      testing.TB
	home   *testHome
	pty    *os.File
	cmd    *exec.Cmd
	exited chan struct{}
	keys   chan []byte

	mu     sync.Mutex
	raw    bytes.Buffer
	screen *vtScreen
	update chan struct{}
}

type termSize struct{ rows, cols int }

// startHash starts an interactive hash (hash -i) in home's work directory
// on a PTY of size rows x cols (24 x 80 when zero).
func startHash(t testing.TB, home *testHome, size termSize, extraEnv ...string) *hashTerm {
	t.Helper()
	if size.rows == 0 {
		size = termSize{24, 80}
	}
	cmd := exec.Command(hashBinary(t), "-i")
	cmd.Dir = home.work
	cmd.Env = home.env(extraEnv...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(size.rows), Cols: uint16(size.cols)})
	if err != nil {
		t.Fatal(err)
	}
	ht := &hashTerm{
		t: t, home: home, pty: f, cmd: cmd,
		exited: make(chan struct{}),
		keys:   make(chan []byte, 4096),
		screen: newVTScreen(size.cols, size.rows),
		update: make(chan struct{}, 1),
	}
	go func() {
		_ = cmd.Wait()
		close(ht.exited)
	}()
	// Keys go out from one goroutine, so a test typing while hash does not
	// read never stops the reader below (a terminal reads while it writes).
	go func() {
		for k := range ht.keys {
			if _, err := f.Write(k); err != nil {
				return
			}
		}
	}()
	go ht.read()
	t.Cleanup(func() {
		if t.Failed() {
			_ = cmd.Process.Signal(syscall.SIGABRT)
			select {
			case <-ht.exited:
			case <-time.After(2 * time.Second):
			}
		}
		close(ht.keys)
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-ht.exited
		if t.Failed() {
			t.Logf("terminal output:\n%s", home.unhome(ht.plain()))
		}
	})
	return ht
}

func (ht *hashTerm) read() {
	chunk := make([]byte, 32*1024)
	for {
		n, err := ht.pty.Read(chunk)
		if n > 0 {
			ht.mu.Lock()
			ht.raw.Write(chunk[:n])
			replies := ht.screen.feed(chunk[:n])
			ht.mu.Unlock()
			if len(replies) > 0 {
				ht.send(replies)
			}
			select {
			case ht.update <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

func (ht *hashTerm) send(b []byte) {
	defer func() { _ = recover() }() // keys after the cleanup closed the channel
	ht.keys <- b
}

// typeText sends text as typed keys, all at once.
func (ht *hashTerm) typeText(text string) { ht.send([]byte(text)) }

// typeChunks sends text in chunks of n bytes, pause apart.
func (ht *hashTerm) typeChunks(text string, n int, pause time.Duration) {
	for i := 0; i < len(text); i += n {
		ht.send([]byte(text[i:min(i+n, len(text))]))
		time.Sleep(pause)
	}
}

// rawOutput is everything hash wrote to the terminal.
func (ht *hashTerm) rawOutput() string {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	return ht.raw.String()
}

// plain is the output without escape sequences or carriage returns.
func (ht *hashTerm) plain() string {
	return strings.ReplaceAll(stripTerminalANSI(ht.rawOutput()), "\r", "")
}

// waitUntil waits for cond, called on the raw output, to hold.
func (ht *hashTerm) waitUntil(what string, timeout time.Duration, cond func(raw string) bool) {
	ht.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for !cond(ht.rawOutput()) {
		select {
		case <-ht.update:
		case <-ht.exited:
			if cond(ht.rawOutput()) {
				return
			}
			ht.t.Fatalf("hash exited while waiting for %s", what)
		case <-deadline.C:
			ht.t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
	}
}

// waitFor waits for needle in the plain output.
func (ht *hashTerm) waitFor(needle string, timeout time.Duration) {
	ht.t.Helper()
	ht.waitUntil(fmt.Sprintf("%q", needle), timeout, func(raw string) bool {
		return strings.Contains(stripTerminalANSI(raw), needle)
	})
}

// promptMark is the OSC 133 mark hash writes as each prompt starts.
const promptMark = "\x1b]133;A"

func (ht *hashTerm) prompts() int { return strings.Count(ht.rawOutput(), promptMark) }

// waitPrompts waits until hash has shown n prompts.
func (ht *hashTerm) waitPrompts(n int, timeout time.Duration) {
	ht.t.Helper()
	ht.waitUntil(fmt.Sprintf("prompt %d", n), timeout, func(raw string) bool {
		return strings.Count(raw, promptMark) >= n
	})
}

// startupOutput is what hash printed before its first prompt.
func (ht *hashTerm) startupOutput() string {
	raw := ht.rawOutput()
	if i := strings.Index(raw, promptMark); i >= 0 {
		raw = raw[:i]
	}
	return strings.ReplaceAll(stripTerminalANSI(raw), "\r", "")
}

// command types line and Enter at the prompt and returns what it printed:
// the plain output between the submitted line and the next prompt.
func (ht *hashTerm) command(line string, timeout time.Duration) string {
	ht.t.Helper()
	before := ht.prompts()
	if before == 0 {
		ht.t.Fatal("command typed before the first prompt")
	}
	start := len(ht.rawOutput())
	ht.typeText(line + "\r")
	ht.waitPrompts(before+1, timeout)
	out := ht.rawOutput()[start:]
	if i := strings.LastIndex(out, promptMark); i >= 0 {
		out = out[:i]
	}
	// The submitted line is drawn bold and ended with CRLF; output follows.
	if i := strings.Index(out, "\x1b[0m\r\n"); i >= 0 {
		out = out[i+len("\x1b[0m\r\n"):]
	}
	return strings.ReplaceAll(stripTerminalANSI(out), "\r", "")
}

// exit ends hash with the exit builtin and waits for it to end.
func (ht *hashTerm) exit() {
	ht.t.Helper()
	ht.typeText("exit\r")
	select {
	case <-ht.exited:
	case <-time.After(5 * time.Second):
		ht.t.Fatal("hash did not exit within 5s of exit")
	}
}

// submittedLines are the lines hash ran from the prompt, as it drew them
// on Enter (bold, then CRLF).
func (ht *hashTerm) submittedLines() []string {
	var lines []string
	for _, m := range submittedLine.FindAllStringSubmatch(ht.rawOutput(), -1) {
		lines = append(lines, m[1])
	}
	return lines
}

var submittedLine = regexp.MustCompile(`\x1b\[1m(.*?)\x1b\[0m\r\n`)

// outputLines are the plain output's lines that match pattern in full.
func (ht *hashTerm) outputLines(pattern string) []string {
	re := regexp.MustCompile(`^(?:` + pattern + `)$`)
	var lines []string
	for _, line := range strings.Split(ht.plain(), "\n") {
		if re.MatchString(line) {
			lines = append(lines, line)
		}
	}
	return lines
}

// scrolled is how many rows the terminal pushed off its top so far.
func (ht *hashTerm) scrolled() int {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	return ht.screen.scrolled
}

// screenLines is the screen's text, one string per row.
func (ht *hashTerm) screenLines() []string {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	lines := make([]string, ht.screen.height)
	for r := range lines {
		lines[r] = ht.screen.line(r)
	}
	return lines
}

// settle waits until hash has written nothing for quiet.
func (ht *hashTerm) settle(quiet time.Duration) {
	for {
		select {
		case <-ht.update:
		case <-time.After(quiet):
			return
		}
	}
}

var _ io.Writer = (*vtScreen)(nil)
