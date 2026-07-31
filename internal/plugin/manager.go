package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PluginRoots returns only the user and system data locations permitted for
// plugin discovery. Repository-local directories are intentionally excluded.
func PluginRoots() []string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	roots := make([]string, 0, 3)
	if dataHome != "" {
		roots = append(roots, filepath.Join(dataHome, "hash", "plugins"))
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, dir := range strings.Split(dataDirs, ":") {
		if dir != "" {
			roots = append(roots, filepath.Join(dir, "hash", "plugins"))
		}
	}
	return roots
}

// Manager selects enabled plugins in configuration order and owns their warm
// process clients for one interactive Hash session.
type Manager struct {
	mu      sync.RWMutex
	enabled []*managedPlugin
}

type managedPlugin struct {
	manifest Manifest
	settings map[string]any
	client   *ProcessClient
}

// NewManager validates enablement against the discovered manifests. Ordering is
// preserved exactly as it appears in [plugins].enabled.
func NewManager(manifests []Manifest, enabled []string, settings map[string]map[string]any) (*Manager, error) {
	available := make(map[string]Manifest, len(manifests))
	for _, manifest := range manifests {
		if _, exists := available[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate plugin ID %q", manifest.ID)
		}
		available[manifest.ID] = manifest
	}
	manager := &Manager{}
	seen := make(map[string]struct{}, len(enabled))
	for _, id := range enabled {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("plugin %q is enabled more than once", id)
		}
		seen[id] = struct{}{}
		manifest, exists := available[id]
		if !exists {
			return nil, fmt.Errorf("enabled plugin %q was not discovered", id)
		}
		pluginSettings := map[string]any{}
		for key, value := range settings[id] {
			pluginSettings[key] = value
		}
		manager.enabled = append(manager.enabled, &managedPlugin{manifest: manifest, settings: pluginSettings})
	}
	return manager, nil
}

// DiscoverManager performs installation discovery and applies user enablement.
func DiscoverManager(enabled []string, settings map[string]map[string]any) (*Manager, error) {
	manifests, err := Discover(PluginRoots())
	if err != nil {
		return nil, err
	}
	return NewManager(manifests, enabled, settings)
}

// EnabledIDs reports the configured priority order.
func (m *Manager) EnabledIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, len(m.enabled))
	for i, plugin := range m.enabled {
		ids[i] = plugin.manifest.ID
	}
	return ids
}

// Start launches all selected plugin processes. It is called only after an
// interactive session has completed startup processing.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, plugin := range m.enabled {
		if plugin.client != nil {
			continue
		}
		client, err := StartProcess(ctx, plugin.manifest, plugin.settings)
		if err != nil {
			m.closeLocked()
			return err
		}
		plugin.client = client
	}
	return nil
}

// Notify broadcasts an observe-only hook in enabled-list order. A failed
// plugin is left isolated from the remaining plugins.
func (m *Manager) Notify(method string, params any) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, plugin := range m.enabled {
		if plugin.client != nil {
			_ = plugin.client.Notify(method, params)
		}
	}
}

// CallFirst asks enabled plugins that declare method, in priority order, until
// a handler returns a result. The caller supplies the hook deadline.
func (m *Manager) CallFirst(ctx context.Context, method string, params any, result any) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, plugin := range m.enabled {
		if plugin.client == nil || !declaresHook(plugin.manifest, method) {
			continue
		}
		if err := plugin.client.Call(ctx, method, params, result); err != nil {
			continue
		}
		return true, nil
	}
	return false, nil
}

func declaresHook(manifest Manifest, method string) bool {
	for _, hook := range manifest.Hooks {
		if hook == method {
			return true
		}
	}
	return false
}

// Close sends shutdown to all warm processes and releases them.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
	return nil
}

func (m *Manager) closeLocked() {
	for _, plugin := range m.enabled {
		if plugin.client != nil {
			_ = plugin.client.Close()
			plugin.client = nil
		}
	}
}
