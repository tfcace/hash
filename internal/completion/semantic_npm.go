package completion

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

// NPMHandler provides completions for npm/yarn/pnpm run scripts.
type NPMHandler struct {
	readFile func(path string) ([]byte, error)
}

// NewNPMHandler creates an npm completion handler.
func NewNPMHandler() *NPMHandler {
	return &NPMHandler{readFile: os.ReadFile}
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

	scripts := h.parsePackageJSON()
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

func (h *NPMHandler) parsePackageJSON() map[string]string {
	data, err := h.readFile("package.json")
	if err != nil {
		return nil
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Scripts
}
