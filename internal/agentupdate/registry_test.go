package agentupdate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistry_Latest(t *testing.T) {
	var gotURI, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"name":"@agentclientprotocol/claude-agent-acp","version":"0.88.0"}`)
	}))
	defer srv.Close()

	v, err := Registry{BaseURL: srv.URL, UserAgent: "hash/test"}.Latest(context.Background(), Package)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if v != "0.88.0" {
		t.Errorf("Latest() = %q, want 0.88.0", v)
	}
	if gotURI != "/@agentclientprotocol%2Fclaude-agent-acp/latest" {
		t.Errorf("request URI = %q, want the scoped package escaped", gotURI)
	}
	if gotUA != "hash/test" {
		t.Errorf("User-Agent = %q", gotUA)
	}
}

func TestRegistry_LatestErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"not found":  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusNotFound) },
		"bad json":   func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>") },
		"no version": func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"name":"x"}`) },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			if _, err := (Registry{BaseURL: srv.URL}).Latest(context.Background(), Package); err == nil {
				t.Error("Latest() error = nil, want an error")
			}
		})
	}
}
