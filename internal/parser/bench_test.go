package parser

import "testing"

// BenchmarkParse measures the per-line dispatch parse with tracing disabled,
// which is how every interactive shell session runs it.
func BenchmarkParse(b *testing.B) {
	cases := []struct{ name, line string }{
		{"regular", "git commit -m 'update readme'"},
		{"agent_prefix", "?? find large files in this directory"},
		{"agent_pipe", "dmesg | ?? what does this error mean"},
		{"agent_inline", "kubectl get pods --sort-by=?? most recent first"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Parse(c.line)
			}
		})
	}
}
