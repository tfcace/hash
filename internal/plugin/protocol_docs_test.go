package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolSchemaAndGuideDocumentEveryHookAndService(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "plugins")
	schema, err := os.ReadFile(filepath.Join(root, "protocol-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("protocol schema is invalid JSON: %v", err)
	}
	guide, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range append(append([]string{}, HookMethods...), HostServiceMethods...) {
		if !strings.Contains(string(schema), method) {
			t.Errorf("schema does not enumerate %s", method)
		}
		if !strings.Contains(string(guide), "`"+method+"`") {
			t.Errorf("developer guide does not document %s", method)
		}
	}
}
