package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tfcace/hash/internal/history"
)

// CommandSuggestor finds similar commands for typos.
type CommandSuggestor struct {
	pathCache      []string
	pathCacheReady atomic.Bool
	pathCacheMu    sync.RWMutex
	historyStore   *history.Store
	packageManager string
	pmOnce         sync.Once
}

// NewCommandSuggestor creates a new suggestor and starts PATH caching in background.
func NewCommandSuggestor(historyStore *history.Store) *CommandSuggestor {
	s := &CommandSuggestor{
		historyStore: historyStore,
		// packageManager detected lazily to allow PATH to be set up first
	}
	go s.buildPathCache()
	return s
}

// getPackageManager returns the detected package manager, detecting lazily on first call.
func (s *CommandSuggestor) getPackageManager() string {
	s.pmOnce.Do(func() {
		s.packageManager = detectPackageManager()
	})
	return s.packageManager
}

// Suggest returns up to 3 similar command names.
func (s *CommandSuggestor) Suggest(cmd string) []string {
	var candidates []string

	// Priority 1: History commands
	if s.historyStore != nil {
		if cmds := s.getHistoryCommands(); len(cmds) > 0 {
			candidates = append(candidates, cmds...)
		}
	}

	// Priority 2: PATH cache (if ready)
	if s.pathCacheReady.Load() {
		s.pathCacheMu.RLock()
		candidates = append(candidates, s.pathCache...)
		s.pathCacheMu.RUnlock()
	}

	// Deduplicate
	candidates = dedupe(candidates)

	return findSimilar(cmd, candidates, 3)
}

// InstallHint returns install instructions if known, empty otherwise.
func (s *CommandSuggestor) InstallHint(cmd string) string {
	hint, ok := installHints[cmd]
	if !ok {
		return ""
	}

	switch s.getPackageManager() {
	case "brew":
		return hint.brew
	case "apt":
		return hint.apt
	case "dnf":
		return hint.dnf
	case "pacman":
		return hint.pacman
	default:
		return ""
	}
}

// buildPathCache scans PATH directories for executables.
func (s *CommandSuggestor) buildPathCache() {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		s.pathCacheReady.Store(true)
		return
	}

	seen := make(map[string]bool)
	var executables []string

	for _, dir := range filepath.SplitList(pathEnv) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if seen[name] {
				continue
			}

			// Check if executable
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode()&0111 != 0 {
				seen[name] = true
				executables = append(executables, name)
			}
		}
	}

	s.pathCacheMu.Lock()
	s.pathCache = executables
	s.pathCacheMu.Unlock()
	s.pathCacheReady.Store(true)
}

// getHistoryCommands extracts unique command names from history.
func (s *CommandSuggestor) getHistoryCommands() []string {
	cmds, err := s.historyStore.GetRecent(500)
	if err != nil {
		return nil //nolint:nilerr // graceful degradation: return empty if history unavailable
	}

	seen := make(map[string]bool)
	var result []string
	for _, cmd := range cmds {
		parts := strings.Fields(cmd.Command)
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result
}

// damerauLevenshtein calculates the Damerau-Levenshtein distance between two strings.
func damerauLevenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Create matrix
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			d[i][j] = min(
				d[i-1][j]+1,      // deletion
				d[i][j-1]+1,      // insertion
				d[i-1][j-1]+cost, // substitution
			)

			// Transposition
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+cost)
			}
		}
	}

	return d[la][lb]
}

// maxDistance returns the maximum edit distance allowed for a command.
func maxDistance(cmd string) int {
	if len(cmd) <= 4 {
		return 1
	}
	return 2
}

// findSimilar finds commands within edit distance threshold, sorted by distance.
func findSimilar(cmd string, candidates []string, maxResults int) []string {
	maxDist := maxDistance(cmd)

	type match struct {
		name string
		dist int
	}
	var matches []match

	for _, c := range candidates {
		if c == cmd {
			continue
		}
		dist := damerauLevenshtein(cmd, c)
		if dist <= maxDist {
			matches = append(matches, match{name: c, dist: dist})
		}
	}

	// Sort by distance, then alphabetically
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].dist != matches[j].dist {
			return matches[i].dist < matches[j].dist
		}
		return matches[i].name < matches[j].name
	})

	// Take top N
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.name
	}
	return result
}

// dedupe removes duplicates from a slice while preserving order.
func dedupe(s []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

// detectPackageManager detects the system's package manager.
func detectPackageManager() string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			return "brew"
		}
	case "linux":
		if _, err := exec.LookPath("apt"); err == nil {
			return "apt"
		}
		if _, err := exec.LookPath("dnf"); err == nil {
			return "dnf"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "pacman"
		}
	}
	return ""
}

// installHint contains install commands for different package managers.
type installHint struct {
	brew   string
	apt    string
	dnf    string
	pacman string
}

// installHints maps command names to install instructions.
var installHints = map[string]installHint{
	// Common CLI tools
	"jq":       {brew: "brew install jq", apt: "apt install jq", dnf: "dnf install jq", pacman: "pacman -S jq"},
	"yq":       {brew: "brew install yq", apt: "apt install yq", dnf: "dnf install yq", pacman: "pacman -S yq"},
	"rg":       {brew: "brew install ripgrep", apt: "apt install ripgrep", dnf: "dnf install ripgrep", pacman: "pacman -S ripgrep"},
	"fd":       {brew: "brew install fd", apt: "apt install fd-find", dnf: "dnf install fd-find", pacman: "pacman -S fd"},
	"bat":      {brew: "brew install bat", apt: "apt install bat", dnf: "dnf install bat", pacman: "pacman -S bat"},
	"eza":      {brew: "brew install eza", apt: "apt install eza", dnf: "dnf install eza", pacman: "pacman -S eza"},
	"exa":      {brew: "brew install exa", apt: "apt install exa", dnf: "dnf install exa", pacman: "pacman -S exa"},
	"fzf":      {brew: "brew install fzf", apt: "apt install fzf", dnf: "dnf install fzf", pacman: "pacman -S fzf"},
	"htop":     {brew: "brew install htop", apt: "apt install htop", dnf: "dnf install htop", pacman: "pacman -S htop"},
	"tree":     {brew: "brew install tree", apt: "apt install tree", dnf: "dnf install tree", pacman: "pacman -S tree"},
	"wget":     {brew: "brew install wget", apt: "apt install wget", dnf: "dnf install wget", pacman: "pacman -S wget"},
	"watch":    {brew: "brew install watch", apt: "apt install procps", dnf: "dnf install procps-ng", pacman: "pacman -S procps-ng"},
	"tldr":     {brew: "brew install tldr", apt: "apt install tldr", dnf: "dnf install tldr", pacman: "pacman -S tldr"},
	"ncdu":     {brew: "brew install ncdu", apt: "apt install ncdu", dnf: "dnf install ncdu", pacman: "pacman -S ncdu"},
	"tmux":     {brew: "brew install tmux", apt: "apt install tmux", dnf: "dnf install tmux", pacman: "pacman -S tmux"},
	"zoxide":   {brew: "brew install zoxide", apt: "apt install zoxide", dnf: "dnf install zoxide", pacman: "pacman -S zoxide"},
	"starship": {brew: "brew install starship", apt: "apt install starship", dnf: "dnf install starship", pacman: "pacman -S starship"},

	// Dev tools
	"node":    {brew: "brew install node", apt: "apt install nodejs", dnf: "dnf install nodejs", pacman: "pacman -S nodejs"},
	"npm":     {brew: "brew install node", apt: "apt install npm", dnf: "dnf install npm", pacman: "pacman -S npm"},
	"python3": {brew: "brew install python3", apt: "apt install python3", dnf: "dnf install python3", pacman: "pacman -S python"},
	"pip":     {brew: "brew install python3", apt: "apt install python3-pip", dnf: "dnf install python3-pip", pacman: "pacman -S python-pip"},
	"pip3":    {brew: "brew install python3", apt: "apt install python3-pip", dnf: "dnf install python3-pip", pacman: "pacman -S python-pip"},
	"go":      {brew: "brew install go", apt: "apt install golang", dnf: "dnf install golang", pacman: "pacman -S go"},
	"rustc":   {brew: "brew install rust", apt: "apt install rustc", dnf: "dnf install rust", pacman: "pacman -S rust"},
	"cargo":   {brew: "brew install rust", apt: "apt install cargo", dnf: "dnf install cargo", pacman: "pacman -S rust"},

	// Container/k8s
	"docker":  {brew: "brew install --cask docker", apt: "apt install docker.io", dnf: "dnf install docker", pacman: "pacman -S docker"},
	"kubectl": {brew: "brew install kubectl", apt: "apt install kubectl", dnf: "dnf install kubectl", pacman: "pacman -S kubectl"},
	"helm":    {brew: "brew install helm", apt: "apt install helm", dnf: "dnf install helm", pacman: "pacman -S helm"},
	"k9s":     {brew: "brew install k9s", apt: "snap install k9s", dnf: "dnf install k9s", pacman: "pacman -S k9s"},
}
