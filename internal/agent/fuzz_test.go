package agent

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// FuzzParseAgentResponse tests response parsing with arbitrary input.
// Run with: go test -fuzz=FuzzParseAgentResponse -fuzztime=30s ./internal/agent/...
func FuzzParseAgentResponse(f *testing.F) {
	seeds := []string{
		// Empty
		"",
		" ",
		"\n",
		"\t",

		// Commands
		"ls -la",
		"find . -name '*.go'",
		"git push origin main",
		"docker run -it ubuntu bash",
		"kubectl get pods -n production",
		"./script.sh",
		"/usr/bin/python3 main.py",

		// Explanations
		"This is an explanation.",
		"The command will find all files larger than 100MB in the current directory.",
		"You can use grep to filter the output. Here's how it works.",
		"I'll help you with that. First, let me explain the concept.",

		// Edge cases
		"find",
		"x",
		string(make([]byte, 100)),
		"line1\nline2\nline3",

		// Unicode
		"echo '日本語'",
		"Это команда для поиска файлов",
		"🚀 deploy",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			return
		}

		// Should never panic
		resp := parseAgentResponse(input)

		// Response type should be valid
		if resp.Type != ResponseTypeCommand &&
			resp.Type != ResponseTypeExplanation &&
			resp.Type != ResponseTypeError {
			t.Errorf("parseAgentResponse(%q) returned invalid type: %v", input, resp.Type)
		}

		// Verify consistency: calling twice should give same result
		resp2 := parseAgentResponse(input)
		if resp.Type != resp2.Type || resp.Command != resp2.Command || resp.Explanation != resp2.Explanation {
			t.Errorf("parseAgentResponse(%q) not deterministic", input)
		}
	})
}

// FuzzLooksLikeCommand tests command detection with arbitrary input.
// Run with: go test -fuzz=FuzzLooksLikeCommand -fuzztime=30s ./internal/agent/...
func FuzzLooksLikeCommand(f *testing.F) {
	seeds := []string{
		"",
		"ls",
		"ls -la",
		"This is an explanation",
		"find . -name '*.go' -type f",
		"The answer is 42",
		"git commit -m 'initial'",
		"I think you should use grep",
		"\n\n\n",
		"a",
		string(make([]byte, 200)),
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			return
		}

		// Should never panic
		_ = looksLikeCommand(input)

		// Deterministic: same input should always give same result
		result1 := looksLikeCommand(input)
		result2 := looksLikeCommand(input)
		if result1 != result2 {
			t.Errorf("looksLikeCommand(%q) not deterministic: %v vs %v", input, result1, result2)
		}
	})
}

// FuzzBuildPromptWithContext tests prompt building with various contexts.
// Run with: go test -fuzz=FuzzBuildPromptWithContext -fuzztime=30s ./internal/agent/...
func FuzzBuildPromptWithContext(f *testing.F) {
	f.Add("find files", "/home/user", "main", "default", "ls", "PATH=/usr/bin", "", "")
	f.Add("", "", "", "", "", "", "", "")
	f.Add("query", "/", "", "", "", "", "previous output", "last error")
	f.Add("日本語のクエリ", "/tmp", "feature/日本語", "", "", "", "", "")

	f.Fuzz(func(t *testing.T, prompt, cwd, gitBranch, kubeCtx, historyCmd, envVar, lastOutput, lastError string) {
		if !utf8.ValidString(prompt) || !utf8.ValidString(cwd) ||
			!utf8.ValidString(gitBranch) || !utf8.ValidString(kubeCtx) ||
			!utf8.ValidString(historyCmd) || !utf8.ValidString(envVar) ||
			!utf8.ValidString(lastOutput) || !utf8.ValidString(lastError) {
			return
		}

		var history []string
		if historyCmd != "" {
			history = []string{historyCmd}
		}

		envVars := make(map[string]string)
		if envVar != "" {
			envVars["TEST_VAR"] = envVar
		}

		req := Request{
			Prompt: prompt,
			Context: Context{
				Cwd:         cwd,
				GitBranch:   gitBranch,
				KubeContext: kubeCtx,
				History:     history,
				EnvVars:     envVars,
				LastOutput:  lastOutput,
				LastError:   lastError,
			},
		}

		// Should never panic
		result := buildPromptWithContext(req)

		// Result should be valid UTF-8
		if !utf8.ValidString(result) {
			t.Errorf("buildPromptWithContext produced invalid UTF-8")
		}

		// Result should contain the prompt (unless prompt is empty)
		if prompt != "" && len(result) < len(prompt) {
			t.Errorf("buildPromptWithContext result shorter than prompt")
		}
	})
}

// FuzzJSONRPCMessage tests JSON-RPC message parsing with malformed input.
// Run with: go test -fuzz=FuzzJSONRPCMessage -fuzztime=30s ./internal/agent/...
func FuzzJSONRPCMessage(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"jsonrpc":"2.0"}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{}}`,
		`{"jsonrpc":"2.0","id":null}`,
		`{"jsonrpc":"2.0","id":"string-id"}`,
		`not json at all`,
		`{"partial":`,
		`[]`,
		`null`,
		`""`,
		`123`,
		`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"abc-123"}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`,
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Test parsing JSON-RPC response structure
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *jsonRPCError   `json:"error"`
		}

		// Should never panic, errors are expected
		_ = json.Unmarshal([]byte(input), &msg)

		// Also test session update params parsing
		var updateParams sessionUpdateParams
		_ = json.Unmarshal([]byte(input), &updateParams)

		// Test new session result parsing
		var sessionResult newSessionResult
		_ = json.Unmarshal([]byte(input), &sessionResult)
	})
}

// FuzzStreamCollector tests stream collection with arbitrary chunks.
// Run with: go test -fuzz=FuzzStreamCollector -fuzztime=30s ./internal/agent/...
func FuzzStreamCollector(f *testing.F) {
	f.Add("hello", " ", "world")
	f.Add("", "", "")
	f.Add("find . -name ", "'*.go'", "")
	f.Add("This is ", "a long ", "explanation.")
	f.Add("日本語", "テスト", "データ")

	f.Fuzz(func(t *testing.T, chunk1, chunk2, chunk3 string) {
		if !utf8.ValidString(chunk1) || !utf8.ValidString(chunk2) || !utf8.ValidString(chunk3) {
			return
		}

		c := NewStreamCollector()

		// Should never panic
		c.Append(chunk1)
		c.Append(chunk2)
		c.Append(chunk3)

		text := c.Text()
		resp := c.Response()

		// Text should be concatenation of chunks
		expected := chunk1 + chunk2 + chunk3
		if text != expected {
			t.Errorf("Text() = %q, want %q", text, expected)
		}

		// Response should be valid
		if resp.Type != ResponseTypeCommand &&
			resp.Type != ResponseTypeExplanation &&
			resp.Type != ResponseTypeError {
			t.Errorf("Response() returned invalid type: %v", resp.Type)
		}
	})
}
