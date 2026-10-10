package agentupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Package is the npm package hash knows how to update.
const Package = "@agentclientprotocol/claude-agent-acp"

// DisplayName is how the package is named to the user.
const DisplayName = "Claude Agent"

// Install describes an npm-installed adapter found behind the configured
// agent command.
type Install struct {
	Version     string    // from the package's package.json
	Dir         string    // package root (.../node_modules/@agentclientprotocol/claude-agent-acp)
	BinPath     string    // the file the command resolves to, symlinks followed
	InstalledAt time.Time // package.json mtime: when npm wrote this version
}

// Detect resolves the first word of command on PATH, follows symlinks, and
// reports the npm package it lives in when that package is Package. Any
// other layout (a different agent, a source checkout, a wrapper script) is
// not an Install hash can update, so ok is false. lookPath is exec.LookPath
// in production.
func Detect(command string, lookPath func(string) (string, error)) (Install, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return Install{}, false
	}
	path, err := lookPath(fields[0])
	if err != nil {
		return Install{}, false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Install{}, false
	}
	sep := string(filepath.Separator)
	marker := sep + filepath.Join("node_modules", filepath.FromSlash(Package)) + sep
	idx := strings.Index(resolved, marker)
	if idx < 0 {
		return Install{}, false
	}
	dir := resolved[:idx+len(marker)-1]
	manifest := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		return Install{}, false
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err = json.Unmarshal(data, &pkg); err != nil || pkg.Name != Package || pkg.Version == "" {
		return Install{}, false
	}
	info, err := os.Stat(manifest)
	if err != nil {
		return Install{}, false
	}
	return Install{Version: pkg.Version, Dir: dir, BinPath: resolved, InstalledAt: info.ModTime()}, true
}
