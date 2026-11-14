package completion

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NPMHandler provides completions for npm/yarn/pnpm run scripts.
type NPMHandler struct {
	readFile func(path string) ([]byte, error)
	cacheTTL time.Duration
	scripts  flightCache[map[string]string] // keyed by directory
}

// NewNPMHandler creates an npm completion handler.
func NewNPMHandler() *NPMHandler {
	return &NPMHandler{
		readFile: os.ReadFile,
		cacheTTL: 2 * time.Second,
	}
}

func (h *NPMHandler) setReady(n *readyNotifier) { h.scripts.ready = n }

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

	scripts, status := h.parsePackageJSON(ctx)
	if status == lookupPending {
		return pendingResult()
	}
	if len(scripts) == 0 {
		return Result{}
	}

	var result Result
	for name, script := range scripts {
		if current == "" || strings.HasPrefix(name, current) {
			if !result.add(Item{Value: name, Display: name, Description: script}) {
				break
			}
		}
	}
	return result
}

// parsePackageJSON returns the scripts of the package.json in the working
// directory. A missing or invalid file is remembered like any other failure,
// for cacheTTL. The returned map is shared with the cache and must not be
// modified.
func (h *NPMHandler) parsePackageJSON(ctx context.Context) (map[string]string, lookupStatus) {
	dir := defaultGetwd()
	policy := flightPolicy{
		ttl:        h.cacheTTL,
		failureTTL: h.cacheTTL,
		timeout:    fileSourceTimeout,
		wait:       sourceWait,
	}
	return h.scripts.get(ctx, dir, policy, func(context.Context) (map[string]string, error) {
		data, err := h.readFile(filepath.Join(dir, "package.json"))
		if err != nil {
			return nil, err
		}
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, err
		}
		return pkg.Scripts, nil
	})
}
