package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests protect the reference runtime contract documented in
// spec/0.1/http.md; they are intentionally narrower than conformance coverage.
type httpContractFixture struct {
	server   *Server
	scope    CreateScopeResult
	planner  RegisterAgentResult
	reviewer RegisterAgentResult
}

func newHTTPContractFixture(t *testing.T) *httpContractFixture {
	t.Helper()
	runtimeValue, err := Open(":memory:")
	requireNoError(t, err)
	server := NewServer(runtimeValue, ServerOptions{AdminToken: "contract-admin-token"})
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	scope, err := runtimeValue.CreateScope(context.Background(), CreateScopeInput{ID: "contract"})
	requireNoError(t, err)
	planner, err := runtimeValue.RegisterAgent(context.Background(), scope.ScopeToken, RegisterAgentInput{ID: "planner", DisplayName: "Planner"})
	requireNoError(t, err)
	reviewer, err := runtimeValue.RegisterAgent(context.Background(), scope.ScopeToken, RegisterAgentInput{ID: "reviewer", DisplayName: "Reviewer"})
	requireNoError(t, err)
	requireNoError(t, runtimeValue.LinkAgents(context.Background(), scope.ScopeToken, planner.AgentID, reviewer.AgentID))
	return &httpContractFixture{server: server, scope: scope, planner: planner, reviewer: reviewer}
}

func contractRequest(t *testing.T, fixture *httpContractFixture, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		requireNoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, request)
	return response
}

func TestHTTPContract(t *testing.T) {
	t.Run("health bare + headers", func(t *testing.T) {
		fixture := newHTTPContractFixture(t)
		response := contractRequest(t, fixture, http.MethodGet, "/health", "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("health returned HTTP %d: %s", response.Code, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
		var health Health
		requireNoError(t, json.Unmarshal(response.Body.Bytes(), &health))
		require(t, health.Status == "ready", "unexpected health response: %#v", health)
		var object map[string]json.RawMessage
		requireNoError(t, json.Unmarshal(response.Body.Bytes(), &object))
		if _, enveloped := object["ok"]; enveloped {
			t.Fatal("health response unexpectedly contains an ok envelope field")
		}
	})

	t.Run("register agent 201", func(t *testing.T) {
		fixture := newHTTPContractFixture(t)
		response := contractRequest(t, fixture, http.MethodPost, "/v1/agents", fixture.scope.ScopeToken, map[string]any{
			"id": "implementer", "displayName": "Implementer",
		})
		if response.Code != http.StatusCreated {
			t.Fatalf("register returned HTTP %d: %s", response.Code, response.Body.String())
		}
		var envelope responseEnvelope[RegisterAgentResult]
		requireNoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require(t, envelope.OK && envelope.Result.AgentToken != "", "unexpected register response: %#v", envelope)
	})

	t.Run("list agents array", func(t *testing.T) {
		fixture := newHTTPContractFixture(t)
		response := contractRequest(t, fixture, http.MethodGet, "/v1/agents", fixture.scope.ScopeToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("list returned HTTP %d: %s", response.Code, response.Body.String())
		}
		var envelope struct {
			OK     bool              `json:"ok"`
			Result []json.RawMessage `json:"result"`
		}
		requireNoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require(t, envelope.OK && len(envelope.Result) == 2, "unexpected list response: %#v", envelope)
	})

	t.Run("link left/right", func(t *testing.T) {
		fixture := newHTTPContractFixture(t)
		response := contractRequest(t, fixture, http.MethodPost, "/v1/links", fixture.scope.ScopeToken, map[string]any{
			"left": fixture.planner.AgentID, "right": fixture.reviewer.AgentID,
		})
		if response.Code != http.StatusOK {
			t.Fatalf("link returned HTTP %d: %s", response.Code, response.Body.String())
		}
		var envelope responseEnvelope[map[string]bool]
		requireNoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require(t, envelope.OK && len(envelope.Result) == 1 && envelope.Result["linked"], "unexpected link response: %#v", envelope)

		for name, body := range map[string]any{
			"from/to":       map[string]any{"from": fixture.planner.AgentID, "to": fixture.reviewer.AgentID},
			"missing right": map[string]any{"left": fixture.planner.AgentID},
		} {
			t.Run(name, func(t *testing.T) {
				response := contractRequest(t, fixture, http.MethodPost, "/v1/links", fixture.scope.ScopeToken, body)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("invalid link returned HTTP %d: %s", response.Code, response.Body.String())
				}
				var envelope responseEnvelope[json.RawMessage]
				requireNoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
				require(t, !envelope.OK && envelope.Error.Code == CodeInvalidArgument, "unexpected invalid link response: %#v", envelope)
			})
		}
	})

	t.Run("send message 202", func(t *testing.T) {
		fixture := newHTTPContractFixture(t)
		response := contractRequest(t, fixture, http.MethodPost, "/v1/messages", fixture.planner.AgentToken, map[string]any{
			"to": fixture.reviewer.AgentID, "body": "Review this", "mode": "request",
		})
		if response.Code != http.StatusAccepted {
			t.Fatalf("send returned HTTP %d: %s", response.Code, response.Body.String())
		}
		var envelope responseEnvelope[DeliveryReceipt]
		requireNoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require(t, envelope.OK && envelope.Result.MessageID != "" && envelope.Result.State == DeliveryQueued, "unexpected send response: %#v", envelope)
	})
}
