// Package plugin implements Hash's external plugin host.
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	// ManifestFilename is the required manifest name for a plugin bundle.
	ManifestFilename = "hash-plugin.toml"
	// ProtocolVersion is the current wire-protocol major version.
	ProtocolVersion = 1
)

// HookMethods is the complete protocol-v1 hook surface.
var HookMethods = []string{
	"session.start", "session.stop", "cwd.changed", "prompt.render",
	"editor.suggest", "completion.provide", "command.before", "command.finished",
	"history.added", "command.execute",
}

// HostServiceMethods is the complete protocol-v1 host-service surface.
var HostServiceMethods = []string{
	"host.history.query", "host.completion.query", "host.environment.get", "host.output.write",
}

var pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)
var pluginCommandPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Manifest describes an installed plugin bundle.
type Manifest struct {
	ManifestVersion int          `toml:"manifest_version"`
	ID              string       `toml:"id"`
	Name            string       `toml:"name"`
	Version         string       `toml:"version"`
	ProtocolVersion int          `toml:"protocol_version"`
	Entrypoint      string       `toml:"entrypoint"`
	Hooks           []string     `toml:"hooks"`
	Commands        []string     `toml:"commands"`
	Capabilities    Capabilities `toml:"capabilities"`
	Directory       string       `toml:"-"`
}

// Capabilities documents the Hash data and services a plugin expects to use.
// Plugin executables are trusted local processes; this is not an OS sandbox.
type Capabilities struct {
	Context      []string `toml:"context"`
	HostServices []string `toml:"host_services"`
}

// LoadManifest reads and validates a manifest in dir.
func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, ManifestFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest
	if err := toml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", path, err)
	}
	manifest.Directory = dir
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return manifest, nil
}

// Executable returns the bundle-local program selected by Entrypoint. The
// manifest keeps the declared relative path so it can be revalidated before
// every launch.
func (m Manifest) Executable() string {
	return filepath.Join(m.Directory, m.Entrypoint)
}

// Validate checks that a manifest is safe to launch from its bundle directory.
func (m Manifest) Validate() error {
	if m.ManifestVersion != 1 {
		return fmt.Errorf("unsupported manifest_version %d", m.ManifestVersion)
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !pluginIDPattern.MatchString(m.ID) {
		return fmt.Errorf("id must be a lowercase reverse-DNS identifier")
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if m.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol_version %d", m.ProtocolVersion)
	}
	if strings.TrimSpace(m.Entrypoint) == "" {
		return fmt.Errorf("entrypoint is required")
	}
	if filepath.IsAbs(m.Entrypoint) || containsParentDirectory(m.Entrypoint) {
		return fmt.Errorf("entrypoint must stay inside the plugin bundle")
	}
	if err := validateDeclaredNames("hook", m.Hooks, HookMethods, false); err != nil {
		return err
	}
	services := make([]string, len(HostServiceMethods))
	for i, method := range HostServiceMethods {
		services[i] = strings.TrimPrefix(method, "host.")
	}
	if err := validateDeclaredNames("host service", m.Capabilities.HostServices, services, true); err != nil {
		return err
	}
	seenCommands := make(map[string]bool, len(m.Commands))
	for _, command := range m.Commands {
		if !pluginCommandPattern.MatchString(command) {
			return fmt.Errorf("invalid plugin command %q", command)
		}
		if seenCommands[command] {
			return fmt.Errorf("duplicate plugin command %q", command)
		}
		seenCommands[command] = true
	}
	return nil
}

func validateDeclaredNames(kind string, values, allowed []string, trimHostPrefix bool) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = true
	}
	seen := make(map[string]bool, len(values))
	for _, original := range values {
		value := original
		if trimHostPrefix {
			value = strings.TrimPrefix(value, "host.")
		}
		if !allowedSet[value] {
			return fmt.Errorf("unknown %s %q", kind, original)
		}
		if seen[value] {
			return fmt.Errorf("duplicate %s %q", kind, original)
		}
		seen[value] = true
	}
	return nil
}

func containsParentDirectory(path string) bool {
	for _, component := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == filepath.Separator }) {
		if component == ".." {
			return true
		}
	}
	return false
}

// Discover loads manifests found directly below every supplied plugin root.
// A duplicate ID is rejected instead of silently shadowing another bundle.
func Discover(roots []string) ([]Manifest, error) {
	var manifests []Manifest
	byID := make(map[string]Manifest)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			info, err := os.Stat(filepath.Join(root, entry.Name()))
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				continue
			}
			manifest, err := LoadManifest(filepath.Join(root, entry.Name()))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			if previous, exists := byID[manifest.ID]; exists {
				return nil, fmt.Errorf("duplicate plugin ID %q in %s and %s", manifest.ID, previous.Directory, manifest.Directory)
			}
			byID[manifest.ID] = manifest
			manifests = append(manifests, manifest)
		}
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	return manifests, nil
}
