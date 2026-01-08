package completion

import (
	"sort"
	"strings"
)

// FuzzyFilter filters and sorts items by fuzzy match score.
func FuzzyFilter(items []Item, query string) []Item {
	if query == "" {
		return items
	}

	query = strings.ToLower(query)
	var scored []Item

	for _, item := range items {
		score := fuzzyScore(strings.ToLower(item.Value), query)
		if score > 0 {
			item.Score = score
			scored = append(scored, item)
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored
}

// fuzzyScore calculates how well target matches query.
// Returns 0 if no match, higher scores for better matches.
func fuzzyScore(target, query string) int {
	if len(query) == 0 {
		return 1
	}
	if len(target) == 0 {
		return 0
	}

	// Exact match
	if target == query {
		return 1000
	}

	// Prefix match
	if strings.HasPrefix(target, query) {
		return 800 + (100 - len(target)) // Shorter = better
	}

	// For path completion (query ends with /), only allow exact and prefix matches.
	// This prevents "Drive/" from matching "Google Drive/" which would cause
	// an infinite loop when pressing TAB repeatedly on paths with spaces.
	if strings.HasSuffix(query, "/") {
		return 0
	}

	// Contains match - only if query starts at a word boundary
	// (start of string, after space, or after /)
	if idx := strings.Index(target, query); idx > 0 {
		// Check if the match starts at a word boundary
		prevChar := target[idx-1]
		if prevChar == ' ' || prevChar == '/' {
			return 600 + (100 - len(target))
		}
		// Not at word boundary - don't count as a contains match
	}

	// Subsequence match
	score := subsequenceScore(target, query)
	if score > 0 {
		return score
	}

	return 0
}

// subsequenceScore checks if query is a subsequence of target.
// Returns score based on how consecutive the matches are.
func subsequenceScore(target, query string) int {
	qi := 0
	matches := 0
	consecutiveBonus := 0
	lastMatchIdx := -2

	for ti := 0; ti < len(target) && qi < len(query); ti++ {
		if target[ti] == query[qi] {
			matches++
			if ti == lastMatchIdx+1 {
				consecutiveBonus += 10 // Bonus for consecutive matches
			}
			lastMatchIdx = ti
			qi++
		}
	}

	if qi < len(query) {
		return 0 // Not all query chars matched
	}

	// Base score + consecutive bonus - length penalty
	return 400 + consecutiveBonus + (100 - len(target))
}
