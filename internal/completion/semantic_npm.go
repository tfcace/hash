package completion

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// NPMHandler provides completions for npm/yarn/pnpm run scripts.
type NPMHandler struct {
	readFile func(path string) ([]byte, error)
	reader   contextBytesReader
	cacheMu  sync.Mutex
	cacheTTL time.Duration
	cacheKey string
	cacheExp time.Time
	scripts  map[string]string
	now      func() time.Time
}

// NewNPMHandler creates an npm completion handler.
func NewNPMHandler() *NPMHandler {
	return &NPMHandler{
		readFile: os.ReadFile,
		cacheTTL: 2 * time.Second,
		now:      time.Now,
	}
}

// Commands returns the commands this handler supports.
func (h *NPMHandler) Commands() []string {
	return []string{"npm", "yarn", "pnpm"}
}

// Complete returns npm script completions for the "run" subcommand.
func (h *NPMHandler) Complete(ctx context.Context, args []string, current string) Result {
	// Only activate for "run" or "run-script" subcommand
	if len(args) == 0 {
		return Result{}
	}
	subCmd := args[0]
	if subCmd != "run" && subCmd != "run-script" {
		return Result{}
	}

	scripts := h.parsePackageJSON(ctx)
	if len(scripts) == 0 {
		return Result{}
	}

	var items []Item
	for name, script := range scripts {
		if current == "" || strings.HasPrefix(name, current) {
			items = append(items, Item{
				Value:       name,
				Display:     name,
				Description: script,
			})
		}
	}
	return Result{Items: items}
}

func (h *NPMHandler) parsePackageJSON(ctx context.Context) map[string]string {
	cacheKey := h.currentCacheKey()
	if h.cacheTTL > 0 {
		if scripts, ok := h.cachedScripts(cacheKey); ok {
			return scripts
		}
	}

	data, err := h.reader.read(ctx, h.readFile, "package.json")
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if h.cacheTTL > 0 {
			h.storeScripts(cacheKey, nil)
		}
		return nil
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		if h.cacheTTL > 0 {
			h.storeScripts(cacheKey, nil)
		}
		return nil
	}
	if h.cacheTTL > 0 {
		h.storeScripts(cacheKey, pkg.Scripts)
	}
	return pkg.Scripts
}

func (h *NPMHandler) cachedScripts(cacheKey string) (map[string]string, bool) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()

	if h.cacheKey != cacheKey || !h.timeNow().Before(h.cacheExp) {
		return nil, false
	}
	return cloneStringMap(h.scripts), true
}

func (h *NPMHandler) storeScripts(cacheKey string, scripts map[string]string) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()

	h.cacheKey = cacheKey
	h.cacheExp = h.timeNow().Add(h.cacheTTL)
	h.scripts = cloneStringMap(scripts)
}

func (h *NPMHandler) currentCacheKey() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func (h *NPMHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
