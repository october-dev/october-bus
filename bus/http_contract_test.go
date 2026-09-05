package bus

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// HTTP contract tests: focused coverage of the public envelope + status-code
// shapes called out in spec/0.1/http.md and issue #99.

func httpDo(t *testing.T, server *httptest.Server, method, path, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = raw
	}
	req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return resp, envelope
}

func TestHTTPContractHealthBareEnvelope(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	server := httptest.NewServer(NewServer(agents.runtime, ServerOptions{}))
	defer server.Close()

	resp, env := httpDo(t, server, http.MethodGet, "/health", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	// Health is the documented bare-envelope exception: fields at top level,
	// NOT wrapped in {ok, result}.
	if _, ok := env["ok"]; ok {
		t.Fatalf("health must be a bare envelope, got ok wrapper: %#v", env)
	}
	if env["name"] != "october-bus" || env["status"] != "ready" {
		t.Fatalf("unexpected health envelope: %#v", env)
	}
}

func TestHTTPContractRegisterReturns201(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	server := httptest.NewServer(NewServer(agents.runtime, ServerOptions{}))
	defer server.Close()

	resp, env := httpDo(t, server, http.MethodPost, "/v1/agents", agents.scope.ScopeToken,
		map[string]any{"id": "worker", "displayName": "Worker"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", resp.StatusCode)
	}
	if env["ok"] != true {
		t.Fatalf("register envelope ok = %v, want true", env["ok"])
	}
	result, _ := env["result"].(map[string]any)
	if result == nil || result["agentId"] != "worker" {
		t.Fatalf("unexpected register result: %#v", env)
	}
}

func TestHTTPContractListAgentsReturnsArray(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	server := httptest.NewServer(NewServer(agents.runtime, ServerOptions{}))
	defer server.Close()

	resp, env := httpDo(t, server, http.MethodGet, "/v1/agents", agents.scope.ScopeToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list-agents status = %d, want 200", resp.StatusCode)
	}
	result, ok := env["result"].([]any)
	if !ok {
		t.Fatalf("list-agents result is not an array: %#v", env["result"])
	}
	if len(result) != 2 { // planner + reviewer from setupAgents
		t.Fatalf("list-agents returned %d agents, want 2", len(result))
	}
}

func TestHTTPContractLinkInputShape(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	server := httptest.NewServer(NewServer(agents.runtime, ServerOptions{}))
	defer server.Close()

	// Reviewer already links to planner in setupAgents; link the other way is
	// a symmetric no-op, but the contract is about the input shape + envelope.
	resp, env := httpDo(t, server, http.MethodPost, "/v1/links", agents.scope.ScopeToken,
		map[string]any{"left": "planner", "right": "reviewer"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("link status = %d, want 200", resp.StatusCode)
	}
	result, _ := env["result"].(map[string]any)
	if result == nil || result["linked"] != true {
		t.Fatalf("unexpected link result: %#v", env)
	}
}

func TestHTTPContractSendMessageReturns202(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	server := httptest.NewServer(NewServer(agents.runtime, ServerOptions{}))
	defer server.Close()

	resp, env := httpDo(t, server, http.MethodPost, "/v1/messages", agents.plannerToken,
		map[string]any{"to": "reviewer", "body": "Review this", "mode": "request"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send-message status = %d, want 202", resp.StatusCode)
	}
	if env["ok"] != true {
		t.Fatalf("send-message envelope ok = %v, want true", env["ok"])
	}
	result, _ := env["result"].(map[string]any)
	if result == nil || result["messageId"] == "" {
		t.Fatalf("unexpected send-message result: %#v", env)
	}
}
