package completion

import "testing"

// BenchmarkBuiltinPluginSpecs measures the cost of producing the built-in
// specs, paid on every handler-table rebuild (startup and completions reload).
func BenchmarkBuiltinPluginSpecs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if specs := builtinPluginSpecs(); len(specs) == 0 {
			b.Fatal("no built-in specs")
		}
	}
}

// BenchmarkNewPluginCompleter measures completer construction as done at
// shell startup, built-in spec handling included.
func BenchmarkNewPluginCompleter(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if c := NewPluginCompleter(nil); c == nil {
			b.Fatal("nil completer")
		}
	}
}
