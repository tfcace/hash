package agent

import (
	"encoding/json"
	"errors"
	"testing"
)

// A JSON-RPC error reply becomes an RPCError with the kind the agent names
// in its data (claude-agent-acp's data.errorKind), or none.
func TestRPCErrorOf_ReadsTheErrorKind(t *testing.T) {
	for reply, want := range map[string]RPCError{
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"Internal error: Rate limited","data":{"errorKind":"rate_limit"}}}`: {Code: -32603, Message: "Internal error: Rate limited", ErrorKind: "rate_limit"},
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"Authentication required"}}`:                                        {Code: -32000, Message: "Authentication required"},
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"boom","data":"a stack trace"}}`:                                    {Code: -32603, Message: "boom"},
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"boom","data":{"errorKind":7}}}`:                                    {Code: -32603, Message: "boom"},
	} {
		var msg acpMessage
		if err := json.Unmarshal([]byte(reply), &msg); err != nil {
			t.Fatal(err)
		}
		var got *RPCError
		if !errors.As(msg.Error.rpcError(), &got) || *got != want {
			t.Errorf("%s\n = %+v, want %+v", reply, got, want)
		}
	}
}
