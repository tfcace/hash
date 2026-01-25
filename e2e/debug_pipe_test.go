//go:build e2e_live

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shell"
)

func runPipeTest(t *testing.T, name, input, pipeCmd, pipeOutput string) {
	transport := agent.NewACPTransport(agent.ACPConfig{
		Command: "claude-code-acp",
		Args:    []string{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer transport.Close()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)

	clipBuf.AddCommand(pipeCmd)
	clipBuf.SetOutput(pipeOutput)

	parsed := parser.Parse(input)
	t.Logf("Prompt: %q", parsed.AgentPrompt)

	start := time.Now()
	resp, err := handler.HandleRequest(ctx, parsed)
	elapsed := time.Since(start)

	t.Logf("Completed in %v", elapsed)

	if err != nil {
		t.Logf("Error: %v", err)
		return
	}

	t.Logf("Response type: %v", resp.Type)
	if resp.Command != "" {
		t.Logf("Command: %q", resp.Command)
	}
	if resp.Explanation != "" {
		t.Logf("Explanation: %.200s...", resp.Explanation)
	}
	if resp.Error != "" {
		t.Logf("Error: %q", resp.Error)
	}
}

func TestDebug_SimplePipe(t *testing.T) {
	runPipeTest(t, "simple",
		"echo hello | ?? how many characters",
		"echo hello",
		"hello")
}

func TestDebug_CSVPipe(t *testing.T) {
	csv := `name,age,city
Alice,30,New York
Bob,25,San Francisco`

	runPipeTest(t, "csv",
		"cat data.csv | ?? count rows",
		"cat data.csv",
		csv)
}

func TestDebug_JSONPipe(t *testing.T) {
	csv := `name,age,city
Alice,30,New York
Bob,25,San Francisco`

	runPipeTest(t, "json",
		"cat data.csv | ?? convert to json",
		"cat data.csv",
		csv)
}

func TestDebug_JSONPipe_Explicit(t *testing.T) {
	csv := `name,age,city
Alice,30,New York
Bob,25,San Francisco`

	runPipeTest(t, "json_explicit",
		"cat data.csv | ?? what command converts this to json",
		"cat data.csv",
		csv)
}

func TestDebug_JSONPipe_JQ(t *testing.T) {
	csv := `name,age,city
Alice,30,New York
Bob,25,San Francisco`

	runPipeTest(t, "json_jq",
		"cat data.csv | ?? suggest jq command for json",
		"cat data.csv",
		csv)
}
