//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shell"
)

// TestDataProcessing_CSVToJSON tests the CSV to JSON recipe.
// Website promise: cat data.csv | ?? convert to json array of objects
func TestDataProcessing_CSVToJSON(t *testing.T) {
	// Read test data
	testdataPath := filepath.Join("testdata", "data.csv")
	csvData, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	mock := NewScenarioMock().
		OnPipePromptContains("json", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `jq -Rs 'split("\n") | .[1:] | map(split(",")) | map({"name": .[0], "age": .[1], "city": .[2]})' | jq -c`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("cat data.csv")
	clipBuf.SetOutput(string(csvData))

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parsed := parser.Parse("cat data.csv | ?? convert to json array of objects")
	if parsed.Type != parser.CommandTypeAgentPipe {
		t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
	}

	resp, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	if resp.Type != agent.ResponseTypeCommand {
		t.Errorf("Response type = %v, want Command", resp.Type)
	}

	// Verify the command is a valid JSON transformation command
	if resp.Command == "" {
		t.Error("Expected non-empty command response")
	}

	// Verify context was passed correctly
	lastReq, ok := mock.LastRequest()
	if !ok {
		t.Fatal("No request captured")
	}
	if lastReq.Context.LastOutput == "" {
		t.Error("Expected CSV data in LastOutput context")
	}
}

// TestDataProcessing_LogExtraction tests the log parsing recipe.
// Website promise: cat access.log | ?? extract ip and status code, group by status
func TestDataProcessing_LogExtraction(t *testing.T) {
	// Read test data
	testdataPath := filepath.Join("testdata", "access.log")
	logData, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	mock := NewScenarioMock().
		OnPipePromptContains("extract ip", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `awk '{print $1, $9}' | sort | uniq -c | sort -rn`,
		}).
		OnPipePromptContains("group by status", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `awk '{status[$9]++; ips[$9] = ips[$9] " " $1} END {for (s in status) print s": "status[s]" requests from"ips[s]}'`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("cat access.log")
	clipBuf.SetOutput(string(logData))

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name   string
		input  string
		hasCmd bool
	}{
		{
			name:   "extract ip and status",
			input:  "cat access.log | ?? extract ip and status code",
			hasCmd: true,
		},
		{
			name:   "group by status",
			input:  "cat access.log | ?? group by status code",
			hasCmd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentPipe {
				t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if tt.hasCmd && resp.Command == "" {
				t.Error("Expected command response")
			}
		})
	}
}

// TestDataProcessing_MultipleFormats tests various data transformation scenarios.
func TestDataProcessing_MultipleFormats(t *testing.T) {
	mock := NewScenarioMock().
		OnPipePromptContains("yaml", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `yq -p=json -o=yaml`,
		}).
		OnPipePromptContains("xml", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `xmllint --format -`,
		}).
		OnPipePromptContains("table", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `column -t -s,`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name        string
		input       string
		pipeData    string
		wantCommand string
	}{
		{
			name:        "json to yaml",
			input:       `echo '{"foo":"bar"}' | ?? convert to yaml`,
			pipeData:    `{"foo":"bar"}`,
			wantCommand: `yq -p=json -o=yaml`,
		},
		{
			name:        "format xml",
			input:       `curl api | ?? format as xml`,
			pipeData:    `<root><item>value</item></root>`,
			wantCommand: `xmllint --format -`,
		},
		{
			name:        "csv to table",
			input:       `cat data.csv | ?? display as table`,
			pipeData:    "name,age\nAlice,30",
			wantCommand: `column -t -s,`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clipBuf.AddCommand("previous-cmd")
			clipBuf.SetOutput(tt.pipeData)

			parsed := parser.Parse(tt.input)
			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if resp.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", resp.Command, tt.wantCommand)
			}
		})
	}
}

// TestDataProcessing_LargeOutput tests handling of large pipe outputs.
func TestDataProcessing_LargeOutput(t *testing.T) {
	// Generate large output (simulating large log file)
	var largeData string
	for i := 0; i < 1000; i++ {
		largeData += "192.168.1.1 - - [25/Jan/2024:10:00:00 +0000] \"GET /api/test HTTP/1.1\" 200 1234\n"
	}

	mock := NewScenarioMock().
		OnPipePromptContains("count", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `wc -l`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	// Small buffer to test truncation
	clipBuf := clipboard.NewBuffer(100)
	clipBuf.SetMaxOutputSize(4096) // Limit to 4KB
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("cat large.log")
	clipBuf.SetOutput(largeData)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parsed := parser.Parse("cat large.log | ?? count lines")
	resp, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	// Should still get a valid response even with truncated context
	if resp.Command == "" {
		t.Error("Expected command even with large/truncated input")
	}

	// Verify context was truncated appropriately
	lastReq, ok := mock.LastRequest()
	if !ok {
		t.Fatal("No request captured")
	}
	if len(lastReq.Context.LastOutput) > 4096 {
		t.Errorf("LastOutput should be <= 4096 bytes, got %d", len(lastReq.Context.LastOutput))
	}
}
