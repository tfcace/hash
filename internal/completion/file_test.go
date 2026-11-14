package completion

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/shlex"
)

type panicInfoDirEntry struct {
	name string
	mode fs.FileMode
}

func (e panicInfoDirEntry) Name() string      { return e.name }
func (e panicInfoDirEntry) IsDir() bool       { return e.mode.IsDir() }
func (e panicInfoDirEntry) Type() fs.FileMode { return e.mode.Type() }
func (e panicInfoDirEntry) Info() (fs.FileInfo, error) {
	panic("Info should not be called during completion")
}

func TestFileDescriptionDoesNotStatEntryForSize(t *testing.T) {
	got := fileDescription(panicInfoDirEntry{name: "slow.txt"}, plainFile)
	if got != "text" {
		t.Fatalf("fileDescription() = %q, want %q", got, "text")
	}
}

func TestFileCompleter_CurrentDir(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "foo.txt"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "bar.txt"), []byte{}, 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0o755) //nolint:gosec // G301: test directory

	// Change to temp dir
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "cat ", 4)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) < 2 {
		t.Errorf("Items count = %d, want >= 2", len(result.Items))
	}

	// Should contain foo.txt and bar.txt
	values := make(map[string]bool)
	for _, item := range result.Items {
		values[item.Value] = true
	}
	if !values["foo.txt"] {
		t.Error("Missing foo.txt in completions")
	}
	if !values["bar.txt"] {
		t.Error("Missing bar.txt in completions")
	}
}

func TestFileCompleter_LimitsHugeDirectoryResults(t *testing.T) {
	entries := make([]os.DirEntry, 5000)
	for i := range entries {
		entries[i] = panicInfoDirEntry{name: "file-" + strconv.Itoa(i) + ".txt"}
	}

	completer := NewFileCompleter()
	completer.readDir = func(dir string) ([]os.DirEntry, error) {
		return entries, nil
	}
	router := NewRouter()
	router.Register(completer, PriorityFilesystem)

	result, err := router.Complete(context.Background(), "cat ", len("cat "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != completionItemLimit || !result.Hidden().NotShown() {
		t.Fatalf("expected huge directory completions to be capped, got %d items, hidden %+v", len(result.Items), result.Hidden())
	}
}

// Fuzzy mode matches the whole directory: a match past its first 200
// entries (/usr/bin/zipgrep) is found, and the router keeps the best 200.
func TestFileCompleter_FuzzyMatchesPastTheFirst200Entries(t *testing.T) {
	entries := make([]os.DirEntry, 300)
	for i := range entries {
		entries[i] = panicInfoDirEntry{name: fmt.Sprintf("file%03d", i)}
	}
	c := NewFileCompleter()
	c.SetFuzzyMode(true)
	c.getwd = func() string { return "/w" }
	c.readDir = func(string) ([]os.DirEntry, error) { return entries, nil }
	r := NewRouter()
	r.SetFuzzy(true)
	r.Register(c, PriorityFilesystem)

	line := "cat file25"
	got, err := r.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	values := map[string]bool{}
	for _, it := range got.Items {
		values[it.Value] = true
	}
	for _, want := range []string{"file250", "file259", "file025", "file125"} {
		if !values[want] {
			t.Errorf("%q: %s missing from %d items", line, want, len(got.Items))
		}
	}
}

func TestFileCompleter_PartialMatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "makefile"), []byte{}, 0644)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "cat RE", 6)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Items count = %d, want 1", len(result.Items))
	}
	if result.Items[0].Value != "README.md" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "README.md")
	}
}

func TestFileCompleter_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	completer := NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "cd ~/", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if result.Prefix != "~/" || len(result.Items) != 1 || result.Items[0].Value != "projects/" {
		t.Errorf("Complete(cd ~/) = %+v, want ~/projects/", result)
	}
}

func TestFileCompleter_Name(t *testing.T) {
	completer := NewFileCompleter()
	if completer.Name() != "file" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "file")
	}
}

func TestFileCompleter_FuzzyMode(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "context.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "container.yaml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte(""), 0644)

	completer := NewFileCompleter()
	completer.SetFuzzyMode(true)

	// Change to temp dir for completion
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// With fuzzy mode, "cont" should return ALL files (router will filter)
	result, err := completer.Complete(context.Background(), "cat cont", 8)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should return all 4 files, not just prefix matches
	if len(result.Items) != 4 {
		t.Errorf("FuzzyMode should return all candidates, got %d items: %v", len(result.Items), result.Items)
	}
}

func TestFileCompleter_PrefixMode(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "context.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte(""), 0644)

	completer := NewFileCompleter()
	completer.SetFuzzyMode(false) // Default - prefix only

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// Without fuzzy mode, "con" should only return prefix matches
	result, err := completer.Complete(context.Background(), "cat con", 7)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should only return config.toml and context.go (prefix matches)
	if len(result.Items) != 2 {
		t.Errorf("PrefixMode should return only prefix matches, got %d items", len(result.Items))
	}
}

func TestFileCompleter_SymlinkToDirectory(t *testing.T) {
	// Create temp directory with a subdirectory and a symlink to it
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "actual_dir")
	os.Mkdir(subDir, 0o755) //nolint:gosec // G301: test directory
	symlink := filepath.Join(tmpDir, "symlink_dir")
	if err := os.Symlink(subDir, symlink); err != nil {
		t.Skipf("Cannot create symlink: %v", err)
	}

	completer := NewFileCompleter()

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	result, err := completer.Complete(context.Background(), "cd ", 3)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Both actual_dir and symlink_dir should have trailing slashes
	// because symlink_dir points to a directory
	for _, item := range result.Items {
		if item.Value == "symlink_dir/" {
			return // Found it with trailing slash - success
		}
		if item.Value == "symlink_dir" {
			t.Errorf("Symlink to directory should have trailing slash, got %q", item.Value)
			return
		}
	}
	t.Error("symlink_dir not found in completions")
}

// A symlink to a directory completes as a directory, outside cd too; one
// to a file does not, and cd leaves it out.
func TestFileCompleter_SymlinkCompletesAsWhatItPointsAt(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, "actual_dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "actual.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"symlink_dir": "actual_dir", "symlink_file": "actual.txt"} {
		if err := os.Symlink(filepath.Join(tmpDir, target), filepath.Join(tmpDir, link)); err != nil {
			t.Skipf("Cannot create symlink: %v", err)
		}
	}
	completer := NewFileCompleter()
	completer.getwd = func() string { return tmpDir }
	for line, want := range map[string][]string{
		"ls sym": {"symlink_dir/", "symlink_file"},
		"cd sym": {"symlink_dir/"},
	} {
		result, err := completer.Complete(context.Background(), line, len(line))
		if err != nil {
			t.Fatalf("Complete(%q): %v", line, err)
		}
		var got []string
		for _, item := range result.Items {
			got = append(got, item.Value)
			if item.Value == "symlink_dir/" && item.Description != "directory" {
				t.Errorf("Complete(%q): %q described as %q, want directory", line, item.Value, item.Description)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("Complete(%q) = %q, want %q", line, got, want)
		}
	}
}

// A symlink whose target does not answer (a dead mount) does not hold the
// listing up: it completes as a name, and cd still offers it.
func TestFileCompleter_HungSymlinkTargetDoesNotHoldTheListing(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	completer := NewFileCompleter()
	completer.getwd = func() string { return "/w" }
	completer.readDir = func(string) ([]os.DirEntry, error) {
		return []os.DirEntry{
			panicInfoDirEntry{name: "mnt", mode: fs.ModeSymlink},
			panicInfoDirEntry{name: "mine", mode: fs.ModeDir},
		}, nil
	}
	completer.stat = func(string) (fs.FileInfo, error) {
		<-block
		return nil, fs.ErrNotExist
	}
	for line, want := range map[string][]string{"ls m": {"mine/", "mnt"}, "cd m": {"mine/", "mnt/"}} {
		result := tabWithBudget(completer, line)
		var got []string
		for _, item := range result.Items {
			got = append(got, item.Value)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("Complete(%q) = %q (%s), want %q", line, got, result.Outcome(), want)
		}
	}
}

// A hung symlink target is stated once, not once per listing refresh: each
// blocked stat holds a goroutine and an OS thread until the mount answers.
// The links after it still resolve on a later refresh.
func TestFileCompleter_HungSymlinkTargetIsStatedOnce(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	dirInfo, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var hung atomic.Int32
	completer := NewFileCompleter()
	completer.readPolicy.ttl = time.Millisecond
	completer.getwd = func() string { return "/w" }
	completer.readDir = func(string) ([]os.DirEntry, error) {
		return []os.DirEntry{
			panicInfoDirEntry{name: "mnt", mode: fs.ModeSymlink},
			panicInfoDirEntry{name: "mine", mode: fs.ModeSymlink},
		}, nil
	}
	completer.stat = func(path string) (fs.FileInfo, error) {
		if filepath.Base(path) == "mine" {
			return dirInfo, nil
		}
		hung.Add(1)
		<-block
		return nil, fs.ErrNotExist
	}
	var got []string
	for range 40 {
		got = got[:0]
		for _, item := range tabWithBudget(completer, "ls m").Items {
			got = append(got, item.Value)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := hung.Load(); n != 1 {
		t.Errorf("hung target stated %d times over 40 refreshes, want 1", n)
	}
	slices.Sort(got)
	if want := []string{"mine/", "mnt"}; !slices.Equal(got, want) {
		t.Errorf("Complete(%q) = %q, want %q", "ls m", got, want)
	}
}

func TestFileCompleter_CoalescesSlowDirectoryReads(t *testing.T) {
	completer := NewFileCompleter()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	completer.readDir = func(dir string) ([]os.DirEntry, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil, nil
	}
	defer close(release)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel1()
	_, err := completer.Complete(ctx1, "cat ", len("cat "))
	if err != nil {
		t.Fatalf("first Complete() error = %v", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("first completion did not start a directory read")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	_, err = completer.Complete(ctx2, "cat ", len("cat "))
	if err != nil {
		t.Fatalf("second Complete() error = %v", err)
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one in-flight directory read, got %d", got)
	}
}

// A directory read stuck past its timeout (a dead mount) is not doubled by
// the next Tab, which would only hang on the same mount: that Tab returns
// at once.
func TestFileCompleter_HungDirectoryReadIsNotRelaunched(t *testing.T) {
	completer := NewFileCompleter()
	completer.readPolicy.timeout = time.Millisecond
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	completer.readDir = func(dir string) ([]os.DirEntry, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil, nil
	}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel1()
	_, err := completer.Complete(ctx1, "cat ", len("cat "))
	if err != nil {
		t.Fatalf("first Complete() error = %v", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("first completion did not start a directory read")
	}
	time.Sleep(2 * time.Millisecond)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	start := time.Now()
	result, err := completer.Complete(ctx2, "cat ", len("cat "))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("second Complete() error = %v", err)
	}
	if elapsed > 30*time.Millisecond {
		t.Fatalf("Tab on a hung directory took %s, want under 30ms", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("hung directory read launched %d times, want 1", got)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %#v, want none", result.Items)
	}
}

func TestFileCompleter_CdIncludesBrokenSymlinkWithoutFollowingTarget(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Symlink(filepath.Join(tmpDir, "missing"), filepath.Join(tmpDir, "broken_link")); err != nil {
		t.Skipf("Cannot create symlink: %v", err)
	}

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	result, err := completer.Complete(context.Background(), "cd ", 3)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	for _, item := range result.Items {
		if item.Value == "broken_link/" {
			return
		}
	}
	t.Fatalf("broken symlink should be offered as a cd candidate without following target; got %#v", result.Items)
}

func TestFileCompleter_HiddenFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "visible.txt"), []byte{}, 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Default: hidden files should NOT be shown
	result, _ := completer.Complete(ctx, "cat ", 4)
	for _, item := range result.Items {
		if item.Value == ".hidden" {
			t.Error("Hidden files should not be shown by default")
		}
	}

	// Enable hidden files
	completer.SetShowHidden(true)
	result2, _ := completer.Complete(ctx, "cat ", 4)
	found := false
	for _, item := range result2.Items {
		if item.Value == ".hidden" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Hidden files should be shown when enabled")
	}

	// Test: hidden files should be shown when prefix starts with "."
	completer.SetShowHidden(false) // Reset to default
	result3, _ := completer.Complete(ctx, "cat .", 5)
	foundWithDotPrefix := false
	for _, item := range result3.Items {
		if item.Value == ".hidden" {
			foundWithDotPrefix = true
			break
		}
	}
	if !foundWithDotPrefix {
		t.Error("Hidden files should be shown when prefix starts with '.'")
	}
}

func TestFileCompleter_AbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "target.txt"), []byte{}, 0644)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete with absolute path
	result, err := completer.Complete(ctx, "cat "+tmpDir+"/tar", len("cat "+tmpDir+"/tar"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Expected 1 completion for absolute path, got %d", len(result.Items))
	}
	if len(result.Items) > 0 && result.Items[0].Value != "target.txt" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "target.txt")
	}
}

func TestFileCompleter_DirectoryWithTrailingSlash(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "mydir")
	os.Mkdir(subDir, 0o755) //nolint:gosec // G301: test directory
	os.WriteFile(filepath.Join(subDir, "inner.txt"), []byte{}, 0o644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "ls mydir/" should list contents of mydir
	result, err := completer.Complete(ctx, "ls mydir/", 9)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Expected 1 file in mydir/, got %d", len(result.Items))
	}
	if len(result.Items) > 0 && result.Items[0].Value != "inner.txt" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "inner.txt")
	}
}

func TestFileCompleter_CdShowsOnlyDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte{}, 0o644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// "cd " should only show directories, not files
	result, err := completer.Complete(ctx, "cd ", 3)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Expected 1 directory, got %d: %v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "subdir/" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "subdir/")
	}

	// "ls " should show both files and directories
	result2, _ := completer.Complete(ctx, "ls ", 3)
	if len(result2.Items) != 2 {
		t.Errorf("ls should show 2 items (dir + file), got %d", len(result2.Items))
	}
}

func TestFileCompleter_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	emptyDir := filepath.Join(tmpDir, "empty")
	os.Mkdir(emptyDir, 0o755) //nolint:gosec // G301: test directory

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "cd empty/" should return no items (empty dir)
	result, err := completer.Complete(ctx, "cd empty/", 9)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Empty directory should have no completions, got %d", len(result.Items))
	}
}

func TestFileCompleter_NonexistentDirectory(t *testing.T) {
	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete path to nonexistent directory should return empty, not error
	result, err := completer.Complete(ctx, "cd /nonexistent/path/", 21)
	if err != nil {
		t.Fatalf("Should not error on nonexistent path, got %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Nonexistent path should have no completions, got %d", len(result.Items))
	}
}

func TestFileCompleter_CaseInsensitivePrefix(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte{}, 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// "read" should match both "README.md" and "readme.txt" (case insensitive)
	result, _ := completer.Complete(ctx, "cat read", 8)
	if len(result.Items) != 2 {
		t.Errorf("Case-insensitive prefix should match 2 files, got %d", len(result.Items))
	}
}

func TestFileCompleter_RootFilesystem(t *testing.T) {
	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "cd /t" should return prefix "/" not "//"
	result, err := completer.Complete(ctx, "cd /t", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// The prefix should be "/" not "//"
	if result.Prefix != "/" {
		t.Errorf("Prefix = %q, want %q", result.Prefix, "/")
	}

	// Verify items don't have double slashes when combined with prefix
	for _, item := range result.Items {
		fullPath := result.Prefix + item.Value
		if strings.Contains(fullPath, "//") {
			t.Errorf("Full path contains double slash: %q", fullPath)
		}
	}
}

func TestFileCompleter_DotSlashPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, "scripts"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "scripts", "build.sh"), []byte{}, 0755)
	os.WriteFile(filepath.Join(tmpDir, "scripts", "test.sh"), []byte{}, 0755)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "./scripts/buil" should preserve "./" prefix
	result, err := completer.Complete(ctx, "./scripts/buil", 14)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Prefix should be "./scripts/" not "scripts/"
	if result.Prefix != "./scripts/" {
		t.Errorf("Prefix = %q, want %q", result.Prefix, "./scripts/")
	}

	// Verify the full path includes "./"
	for _, item := range result.Items {
		fullPath := result.Prefix + item.Value
		if !strings.HasPrefix(fullPath, "./") {
			t.Errorf("Full path should start with './', got %q", fullPath)
		}
	}
}

func TestFileCompleter_ShellEscapedPathInput(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "My File.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	result, err := completer.Complete(context.Background(), `cat My\ `, len(`cat My\ `))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "My File.txt" {
		t.Fatalf("Value = %q, want the name (the router writes it as shell text)", result.Items[0].Value)
	}
}

func TestFileCompleter_QuotedPathInput(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "My File.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	tests := []string{`cat 'My `, `cat "My `}
	for _, line := range tests {
		t.Run(line, func(t *testing.T) {
			result, err := completer.Complete(context.Background(), line, len(line))
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			if len(result.Items) != 1 {
				t.Fatalf("expected 1 item, got %d: %v", len(result.Items), result.Items)
			}
			if result.Items[0].Value != "My File.txt" {
				t.Fatalf("Value = %q, want the name (the router writes it as shell text)", result.Items[0].Value)
			}
		})
	}
}

func TestFileCompleter_EscapedDirectoryInput(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "My Dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Child File.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	result, err := completer.Complete(context.Background(), `cat My\ Dir/`, len(`cat My\ Dir/`))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if result.Prefix != "My Dir/" {
		t.Fatalf("Prefix = %q, want %q", result.Prefix, "My Dir/")
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "Child File.txt" {
		t.Fatalf("Value = %q, want %q", result.Items[0].Value, "Child File.txt")
	}
}

// slowReadDir lists the real directory after a delay, counting reads.
func slowReadDir(delay time.Duration, reads *atomic.Int32) func(string) ([]os.DirEntry, error) {
	return func(p string) ([]os.DirEntry, error) {
		reads.Add(1)
		time.Sleep(delay)
		return os.ReadDir(p)
	}
}

// tabWithBudget completes line with the editor's 150ms budget.
func tabWithBudget(c Completer, line string) Result {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r, _ := c.Complete(ctx, line, len(line))
	return r
}

// A directory slower than the Tab budget (a network mount) says "fetching"
// and wakes the editor when the listing lands, instead of answering
// nothing and only completing if re-Tabbed within the listing's ttl.
func TestFileCompleter_SlowDirectoryIsPendingAndNotifiesReady(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var reads atomic.Int32
	c := NewFileCompleter()
	c.readDir = slowReadDir(300*time.Millisecond, &reads)
	ready := make(chan struct{}, 4)
	c.SetOnReady(func() { ready <- struct{}{} })

	line := "cat " + dir + "/"
	if r := tabWithBudget(c, line); r.Outcome() != OutcomePending {
		t.Fatalf("Tab on a slow directory = %v, want pending", r.Outcome())
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("no ready notification when the listing landed")
	}
	if r := tabWithBudget(c, line); len(r.Items) != 2 {
		t.Fatalf("retry after ready = %v %#v, want alpha and beta", r.Outcome(), r.Items)
	}
}

// Past its short ttl, a slow directory's last listing is served while it is
// read again, rather than "fetching" on every Tab.
func TestFileCompleter_SlowDirectoryServesItsLastListingWhileRereading(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	var reads atomic.Int32
	c := NewFileCompleter()
	c.reads.now = clock.Now
	c.readDir = slowReadDir(0, &reads)
	line := "cat " + dir + "/"
	if r := tabWithBudget(c, line); len(r.Items) != 1 {
		t.Fatalf("first Tab = %#v, want alpha", r.Items)
	}

	clock.Advance(5 * time.Second)
	c.readDir = slowReadDir(300*time.Millisecond, &reads)
	start := time.Now()
	r := tabWithBudget(c, line)
	if len(r.Items) != 1 || r.Items[0].Value != "alpha" {
		t.Fatalf("Tab while rereading = %v %#v, want the last listing", r.Outcome(), r.Items)
	}
	if elapsed := time.Since(start); elapsed > 140*time.Millisecond {
		t.Fatalf("Tab while rereading took %s, want the last listing within the budget", elapsed)
	}
}

// A reread that hangs past its timeout (a dead network mount) keeps the
// last listing standing in, Tab after Tab, until its grace ends.
func TestFileCompleter_LastListingStandsInWhileARereadIsHung(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	var reads atomic.Int32
	c := NewFileCompleter()
	c.reads.now = clock.Now
	c.readPolicy.timeout = 30 * time.Millisecond
	c.readDir = slowReadDir(0, &reads)
	line := "cat " + dir + "/"
	if r := tabWithBudget(c, line); len(r.Items) != 1 {
		t.Fatalf("first Tab = %#v, want alpha", r.Items)
	}

	clock.Advance(5 * time.Second)
	hang := make(chan struct{})
	defer close(hang)
	c.readDir = func(string) ([]os.DirEntry, error) {
		reads.Add(1)
		<-hang
		return nil, nil
	}
	for i := range 3 {
		if r := tabWithBudget(c, line); len(r.Items) != 1 || r.Items[0].Value != "alpha" {
			t.Fatalf("Tab %d with the reread hung = %v %#v, want the last listing", i, r.Outcome(), r.Items)
		}
	}
	if got := reads.Load(); got != 2 {
		t.Fatalf("reads = %d, want 2 (the first and the one hung reread)", got)
	}
}

// A directory that reads quickly never answers from an old listing: a file
// created since the last Tab must show up.
func TestFileCompleter_FastDirectoryNeverServesAnOutdatedListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	var reads atomic.Int32
	c := NewFileCompleter()
	c.reads.now = clock.Now
	c.readDir = slowReadDir(0, &reads)
	line := "cat " + dir + "/"
	_ = tabWithBudget(c, line)

	if err := os.WriteFile(filepath.Join(dir, "beta"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Second)
	if r := tabWithBudget(c, line); len(r.Items) != 2 {
		t.Fatalf("Tab after a new file appeared = %#v, want alpha and beta", r.Items)
	}
}

// A redirection target glued to its operator (">out", "2>log", "<in")
// completes as a path, with the operator kept in front.
func TestFileCompleter_CompletesGluedRedirectionTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The replacement starts after the operator, which stays.
	router := NewRouter()
	router.Register(NewFileCompleter(), PriorityFilesystem)
	for _, op := range []string{">", ">>", "2>", "<", "&>", ">|"} {
		line := "sort " + op + dir + "/ou"
		r, _ := router.Complete(context.Background(), line, len(line))
		if len(r.Items) != 1 || applied(line, r) != "sort "+op+dir+"/out.txt" {
			t.Errorf("router Complete(%q) = prefix %q items %#v from %d", line, r.Prefix, r.Items, r.ReplaceFrom)
		}
	}

	// Glued to the word before it, the redirection still starts its own
	// word: the replacement starts after the operator, and the word stays.
	for _, op := range []string{">", ">>", "&>", "<"} {
		line := "sort data" + op + dir + "/ou"
		r, _ := router.Complete(context.Background(), line, len(line))
		if len(r.Items) != 1 || applied(line, r) != "sort data"+op+dir+"/out.txt" {
			t.Errorf("router Complete(%q) = prefix %q items %#v from %d", line, r.Prefix, r.Items, r.ReplaceFrom)
		}
	}

	// The router's fuzzy filter matches on the target, not the operator.
	fuzzyFiles := NewFileCompleter()
	fuzzyFiles.SetFuzzyMode(true)
	router = NewRouter()
	router.SetFuzzy(true)
	router.Register(fuzzyFiles, PriorityFilesystem)
	line := "sort >" + dir + "/ou"
	r, _ := router.Complete(context.Background(), line, len(line))
	if len(r.Items) != 1 || applied(line, r) != line[:len(line)-2]+"out.txt" {
		t.Errorf("fuzzy router Complete(%q) = prefix %q items %#v", line, r.Prefix, r.Items)
	}
}

// "$(pwd)/" is one word: the / after the substitution is not the root
// directory. The completer cannot run the substitution, so it has nothing to
// offer rather than the children of /.
func TestFileCompleter_PathAfterASubstitutionIsNotTheRoot(t *testing.T) {
	c := NewFileCompleter()
	var read []string
	c.readDir = func(path string) ([]os.DirEntry, error) {
		read = append(read, path)
		return os.ReadDir(path)
	}
	for _, line := range []string{"ls $(pwd)/", "ls `pwd`/", `ls "$(pwd)"/`} {
		read = nil
		r, _ := c.Complete(context.Background(), line, len(line))
		if len(r.Items) != 0 || slices.Contains(read, "/") {
			t.Errorf("Complete(%q) read %q and offered %d items, want nothing from /", line, read, len(r.Items))
		}
	}
}

// Relative paths are read in the completer's working directory, passed in
// like the other completers' (it is the shell's, which the cd builtin keeps
// as the process's), and the listing is cached under the absolute path so a
// cd never serves another directory's listing.
func TestFileCompleter_ReadsRelativePathsInItsWorkingDirectory(t *testing.T) {
	repoA, repoB := t.TempDir(), t.TempDir()
	for dir, name := range map[string]string{repoA: "alpha", repoB: "beta"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cwd := repoA
	c := NewFileCompleter()
	c.getwd = func() string { return cwd }
	for _, want := range []string{"alpha", "beta"} {
		r, _ := c.Complete(context.Background(), "cat ", len("cat "))
		if len(r.Items) != 1 || r.Items[0].Value != want {
			t.Fatalf("in %s: items %#v, want %s", cwd, r.Items, want)
		}
		cwd = repoB
	}
}

// Reads hung under one dead mount keep the mount's other directories from
// launching more, but directories elsewhere still complete.
func TestFileCompleter_LocalDirectoryCompletesWhileAMountHangs(t *testing.T) {
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "readme.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewFileCompleter()
	c.readPolicy.timeout = 5 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	var deadReads atomic.Int32
	c.readDir = func(dir string) ([]os.DirEntry, error) {
		if strings.HasPrefix(dir, "/dead/") {
			deadReads.Add(1)
			<-release
		}
		return os.ReadDir(dir)
	}
	tab := func(line string) Result {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		result, _ := c.Complete(ctx, line, len(line))
		return result
	}
	for i := range maxHungFlights {
		dir := "/dead/d" + strconv.Itoa(i)
		tab("ls " + dir + "/")
		waitHung(t, &c.reads, dir+"/")
	}

	if result := tab("cat " + local + "/rea"); len(result.Items) != 1 || result.Items[0].Value != "readme.txt" {
		t.Fatalf("local directory with %d reads hung on /dead = %v %#v, want readme.txt", maxHungFlights, result.Outcome(), result.Items)
	}
	if tab("ls /dead/d99/"); deadReads.Load() != maxHungFlights {
		t.Fatalf("reads launched under /dead = %d, want %d: the hung mount takes no more", deadReads.Load(), maxHungFlights)
	}
}

// In a comment or a heredoc body > is text, not a redirection: the word is
// a path that starts with it.
func TestFileCompleter_TextWordIsNotARedirection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"foo.txt", ">fox.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := NewFileCompleter()
	c.getwd = func() string { return dir }
	for _, line := range []string{"echo # >fo", "cat <<EOF\n>fo"} {
		r, _ := c.Complete(context.Background(), line, len(line))
		if len(r.Items) != 1 || r.Prefix != "" || r.Items[0].Display != ">fox.txt" {
			t.Errorf("Complete(%q) = prefix %q items %#v, want >fox.txt alone", line, r.Prefix, r.Items)
		}
	}
}

// The lexer knows no reserved words, and hash's parser does not recover an
// unclosed [[ test or case pattern: a file still completes in one.
func TestFileCompleter_CompletesInAnUnclosedTestOrCasePattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	router.Register(NewFileCompleter(), PriorityFilesystem)
	for _, before := range []string{"[[ -d ", "if [[ -f ", "while [[ -e ", `[[ -n "`, "case x in ", "ls; [[ -d "} {
		line := before + dir + "/s"
		r, _ := router.Complete(context.Background(), line, len(line))
		if len(r.Items) != 1 || applied(line, r) != before+dir+"/src/" {
			t.Errorf("router Complete(%q) = prefix %q items %#v", line, r.Prefix, r.Items)
		}
	}
}

// A typed directory stays as typed, and the kernel resolves its ..: lnk/..
// is the parent of lnk's target, not the directory lnk is in, and
// ~/../work/ is not work/.
func TestFileCompleter_KeepsATypedDirectoryWithDotDot(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cwd", "far/target", "far/only.d", "work/My Docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "far/target"), filepath.Join(root, "cwd/lnk")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "cwd"))
	router := NewRouter()
	c := NewFileCompleter()
	c.getwd = func() string { return filepath.Join(root, "cwd") }
	router.Register(c, PriorityFilesystem)
	for line, want := range map[string]string{
		"ls lnk/../on":    `ls lnk/../only.d/`,
		"ls ~/../work/My": `ls ~/../work/My\ Docs/`,
		"ls ./lnk/../on":  `ls ./lnk/../only.d/`,
		"ls lnk//../on":   `ls lnk//../only.d/`,
	} {
		r, _ := router.Complete(context.Background(), line, len(line))
		if len(r.Items) != 1 || applied(line, r) != want {
			t.Errorf("Complete(%q) = %+v from %d, want %q", line, r.Items, r.ReplaceFrom, want)
		}
	}
}

// A word of dots is a name to match, like any other: "." matches the
// dotfiles, and . and .. themselves (./ and ../, as bash offers them), and
// every item, joined to the text before it, names a path that is there.
func TestFileCompleter_DotWordMatchesDotfilesNotTheListing(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	for _, dir := range []string{".config", "src/.git", "src/lib"} {
		if err := os.MkdirAll(filepath.Join(cwd, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{".README.md", "a.txt", "src/.env"} {
		if err := os.WriteFile(filepath.Join(cwd, file), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wants := map[string][]string{
		"ls .":     {"./", "../", ".config/", ".README.md"},
		"cd .":     {"./", "../", ".config/"},
		"cd ..":    {"../"},
		"ls ./.":   {"./.config/", "./.README.md", "././", "./../"},
		"ls src/.": {"src/.git/", "src/.env", "src/./", "src/../"},
	}
	for _, fuzzy := range []bool{false, true} {
		for _, line := range []string{"ls .", "cd .", "ls ..", "cd ..", "ls ./", "ls ./.", "ls src/.", "cat ", "ls .c"} {
			c := NewFileCompleter()
			c.getwd = func() string { return cwd }
			c.SetFuzzyMode(fuzzy)
			router := NewRouter()
			router.SetFuzzy(fuzzy)
			router.Register(c, PriorityFilesystem)
			r, err := router.Complete(context.Background(), line, len(line))
			if err != nil {
				t.Fatalf("Complete(%q): %v", line, err)
			}
			var got []string
			for _, item := range r.Items {
				path := shlex.Unquote(r.Prefix + item.Value)
				got = append(got, path)
				if _, err := os.Lstat(filepath.Join(cwd, path)); err != nil {
					t.Errorf("fuzzy=%v Complete(%q) offers %q, which is not there", fuzzy, line, path)
				}
			}
			if want, ok := wants[line]; ok {
				for _, w := range want {
					if !slices.Contains(got, w) {
						t.Errorf("fuzzy=%v Complete(%q) = %q, want %q among them", fuzzy, line, got, w)
					}
				}
			}
		}
	}
}
