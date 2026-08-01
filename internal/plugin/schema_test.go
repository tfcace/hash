package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestProtocolMethodSchemasCompileAndValidateCanonicalMessages(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "plugins", "schemas")
	examples := map[string]string{
		"initialize.schema.json":       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol_version":1,"hash_version":"0.9.0","plugin":{"id":"io.runhash.autocorrection","version":"0.1.0"},"hooks":["command.finished"],"settings":{"max_candidates":3},"cwd":"/work","dialect":"bash"}}`,
		"cancellation.schema.json":     `{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":6}}`,
		"command-finished.schema.json": `{"jsonrpc":"2.0","id":6,"method":"command.finished","params":{"generation":42,"original_line":"git sttaus","executed_line":"git sttaus","exit_code":1,"duration_ms":18,"failure_kind":"exit_status","error_message":"","stdout_tail":"","stderr_tail":"git: unknown subcommand sttaus","output_streams_merged":false,"cwd":"/work","dialect":"bash","canceled":false}}`,
		"history-query.schema.json":    `{"jsonrpc":"2.0","id":20,"method":"host.history.query","params":{"parent_request_id":6,"prefix":"git","cwd":"/work","limit":5}}`,
		"completion-query.schema.json": `{"jsonrpc":"2.0","id":21,"method":"host.completion.query","params":{"parent_request_id":6,"line":"git sttaus","cursor":4}}`,
		"error.schema.json":            `{"jsonrpc":"2.0","id":6,"error":{"code":-32602,"message":"invalid params"}}`,
		"shutdown.schema.json":         `{"jsonrpc":"2.0","id":8,"method":"shutdown","params":{}}`,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(examples) {
		t.Fatalf("schema count=%d, examples=%d", len(entries), len(examples))
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			schemaData, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var schemaDocument any
			if err := json.Unmarshal(schemaData, &schemaDocument); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			url := "https://runhash.dev/plugin-v1/" + name
			if err := compiler.AddResource(url, schemaDocument); err != nil {
				t.Fatal(err)
			}
			schema, err := compiler.Compile(url)
			if err != nil {
				t.Fatal(err)
			}
			var message any
			if err := json.Unmarshal([]byte(examples[name]), &message); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(message); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEveryDeveloperGuideJSONBlockMatchesProtocolEnvelope(t *testing.T) {
	docPath := filepath.Join("..", "..", "docs", "plugins", "README.md")
	guide, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join("..", "..", "docs", "plugins", "protocol-v1.schema.json")
	schemaDocument, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(schemaDocument, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://runhash.dev/schemas/plugin-protocol-v1.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```").FindAllSubmatch(guide, -1)
	if len(blocks) == 0 {
		t.Fatal("developer guide has no JSON examples")
	}
	for index, block := range blocks {
		var message any
		if err := json.Unmarshal(block[1], &message); err != nil {
			t.Fatalf("JSON block %d: %v", index+1, err)
		}
		if err := schema.Validate(message); err != nil {
			t.Fatalf("JSON block %d: %v", index+1, err)
		}
	}
}
