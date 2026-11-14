package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/prompt"
)

// promptShell is a shell reading its prompt from a pty, with a history for
// Ctrl+R to search. It returns the pty's shell side, where the user types,
// and a count of the editors that came up ready.
func promptShell(t *testing.T) (sh *Shell, fd int, typing *os.File, ready *atomic.Int32) {
	t.Helper()
	tty, fd, typing := openTTY(t)
	processStdin(t, tty)
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ready = &atomic.Int32{}
	sh = &Shell{
		stdin:      tty,
		responseUI: NewResponseUI(io.Discard),
		prompt:     prompt.New(prompt.Config{Mode: "none"}),
		history:    store,
		editorCfg: editor.Config{
			Input:        editor.NewInputReader(tty),
			OnInputReady: func() { ready.Add(1) },
		},
	}
	return sh, fd, typing, ready
}

// waitPicking waits until a picker owns the keyboard and has the terminal
// raw, signals off, as bubbletea leaves it while it runs.
func waitPicking(t *testing.T, sh *Shell, fd int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tio, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
		if sh.editorCfg.Input.Owner() == "picker" && err == nil && tio.Lflag&unix.ISIG == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no picker took the terminal")
		}
		time.Sleep(time.Millisecond)
	}
}

// Closing the terminal (SIGHUP) or SIGTERM while Ctrl+R or Ctrl+P is open
// ends the shell: the picker ends with the shell's context, and no fresh
// prompt comes up after it. The pickers ran without the context, so hash and
// its agent stayed up forever once the terminal was gone.
func TestReadLine_EndsWhenTheShellIsTerminatedInAPicker(t *testing.T) {
	for name, key := range map[string]string{"Ctrl+R": "\x12", "Ctrl+P": "\x10"} {
		t.Run(name, func(t *testing.T) {
			sh, fd, typing, ready := promptShell(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type read struct {
				line string
				err  error
			}
			done := make(chan read, 1)
			// stdout is swapped here, never while the picker runs.
			captureStdout(t, sh, func() {
				go func() {
					line, err := sh.readLineWithEditor(ctx)
					done <- read{line, err}
				}()
				waitFor(t, func() bool { return ready.Load() == 1 })
				if _, err := typing.WriteString(key); err != nil {
					t.Error(err)
				}
				waitPicking(t, sh, fd)
				cancel()
				select {
				case got := <-done:
					if got.err == nil {
						t.Errorf("readLine = %q, nil; want the shell's ending", got.line)
					}
				case <-time.After(3 * time.Second):
					_, _ = typing.WriteString("\x1b") // let the picker go
					<-done
					t.Error("the picker outlived the shell's context")
				}
			})
			if n := ready.Load(); n != 1 {
				t.Errorf("%d prompts came up; want none after the shell ended", n-1)
			}
		})
	}
}

// The model picker (`model` with no arguments) ends with the shell too:
// it ran without the builtin's context, so closing the terminal while it
// was open left hash and its agent up.
func TestModelPicker_EndsWhenTheShellIsTerminated(t *testing.T) {
	tty, fd, typing := openTTY(t)
	processStdin(t, tty)
	sh := newUpdateShell(t, &modelsTestTransport{models: sampleModels(), current: "default"}, fakeInstaller{})
	sh.stdin = tty
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	captureStdout(t, sh, func() {
		go func() { done <- sh.builtinModel(ctx, nil) }()
		waitFor(t, func() bool {
			tio, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
			return err == nil && tio.Lflag&unix.ISIG == 0
		})
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_, _ = typing.WriteString("\x1b") // let the picker go
			<-done
			t.Error("the model picker outlived the shell's context")
		}
	})
}

// A picker (Ctrl+R, Ctrl+P) opens on the prompt's input row, under its
// info bar, and the next editor comes back on that row: the bar above it
// stays, and is not drawn again. It was, under the one still on screen.
func TestReadLine_APickerLeavesTheInfoBarAsItIs(t *testing.T) {
	for name, key := range map[string]string{"Ctrl+R": "\x12", "Ctrl+P": "\x10"} {
		t.Run(name, func(t *testing.T) {
			sh, fd, typing, ready := promptShell(t)
			const bar = "INFO-BAR"
			starship := filepath.Join(t.TempDir(), "starship")
			if err := os.WriteFile(starship, []byte("#!/bin/sh\nprintf '"+bar+"\\n> '\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			sh.prompt = prompt.New(prompt.Config{Mode: "starship", StarshipPath: starship})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			out := captureStdout(t, sh, func() {
				go func() {
					_, err := sh.readLineWithEditor(ctx)
					done <- err
				}()
				waitFor(t, func() bool { return ready.Load() == 1 })
				_, _ = typing.WriteString(key)
				waitPicking(t, sh, fd)
				_, _ = typing.WriteString("\x1b") // leave the picker
				waitFor(t, func() bool { return ready.Load() == 2 })
				_, _ = typing.WriteString("\r")
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			if n := strings.Count(out, bar); n != 0 {
				t.Errorf("the info bar was drawn %d more times after the picker: %q", n, out)
			}
		})
	}
}
