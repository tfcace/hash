package prompt

import (
	"testing"
)

// BenchmarkBuiltinPrompt measures built-in prompt generation (no subprocess).
func BenchmarkBuiltinPrompt(b *testing.B) {
	p := New(Config{Mode: "built-in"})
	ctx := PromptContext{
		Cwd:      "/home/user/projects/hash",
		ExitCode: 0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Generate(ctx)
	}
}

// BenchmarkBuiltinPromptMultiLine measures GenerateMultiLine for built-in prompt.
func BenchmarkBuiltinPromptMultiLine(b *testing.B) {
	p := New(Config{Mode: "built-in"})
	ctx := PromptContext{
		Cwd:      "/home/user/projects/hash",
		ExitCode: 0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.GenerateMultiLine(ctx)
	}
}

// BenchmarkStarshipPrompt measures starship prompt generation (subprocess per call).
// Skipped if starship is not installed.
func BenchmarkStarshipPrompt(b *testing.B) {
	p := New(Config{Mode: "starship"})
	ctx := PromptContext{
		Cwd:      "/home/user/projects/hash",
		ExitCode: 0,
	}

	// Check if starship is available
	test := p.Generate(ctx)
	if p.starshipPath == "" {
		b.Skip("starship not installed")
	}
	_ = test

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Generate(ctx)
	}
}

// BenchmarkStarshipPromptSameContext benchmarks repeated calls with identical context.
// This is the hot path that caching optimizes — after every command in the same directory
// with the same exit code, the prompt is regenerated identically.
func BenchmarkStarshipPromptSameContext(b *testing.B) {
	p := New(Config{Mode: "starship"})
	ctx := PromptContext{
		Cwd:        "/home/user/projects/hash",
		ExitCode:   0,
		DurationMs: 100,
		Jobs:       0,
	}

	if test := p.Generate(ctx); p.starshipPath == "" {
		b.Skip("starship not installed")
	} else {
		_ = test
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Generate(ctx)
	}
}

// BenchmarkStarshipPromptVaryingContext benchmarks calls with changing context.
// This measures the uncacheable case where exit code or cwd changes.
func BenchmarkStarshipPromptVaryingContext(b *testing.B) {
	p := New(Config{Mode: "starship"})

	contexts := []PromptContext{
		{Cwd: "/home/user/projects/hash", ExitCode: 0, DurationMs: 100},
		{Cwd: "/home/user/projects/hash", ExitCode: 1, DurationMs: 50},
		{Cwd: "/tmp", ExitCode: 0, DurationMs: 200},
		{Cwd: "/home/user", ExitCode: 0, DurationMs: 0},
	}

	if test := p.Generate(contexts[0]); p.starshipPath == "" {
		b.Skip("starship not installed")
	} else {
		_ = test
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Generate(contexts[i%len(contexts)])
	}
}

// BenchmarkCacheHit directly measures the cache lookup path.
// This simulates the common case: user runs commands in the same directory
// and all succeed (exit code 0). Each prompt generation after the first
// should return immediately from cache.
func BenchmarkCacheHit(b *testing.B) {
	p := New(Config{Mode: "starship"})
	ctx := PromptContext{
		Cwd:        "/home/user/projects/hash",
		ExitCode:   0,
		DurationMs: 42,
		Jobs:       0,
	}

	// Prime the cache (uses built-in fallback if starship not installed)
	p.Generate(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Generate(ctx)
	}
}

// BenchmarkCacheMiss measures the forced-miss path by invalidating between calls.
func BenchmarkCacheMiss(b *testing.B) {
	p := New(Config{Mode: "built-in"})
	ctx := PromptContext{
		Cwd:        "/home/user/projects/hash",
		ExitCode:   0,
		DurationMs: 42,
		Jobs:       0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.InvalidateCache()
		p.Generate(ctx)
	}
}
