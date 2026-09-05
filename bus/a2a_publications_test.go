package bus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCardPublicationPersistsAndKeepsStableIdentity(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bus.db")
	agents := setupAgents(t, database)
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	require(t, publication.ID != "" && publication.Enabled && publication.ScopeID == agents.scope.ScopeID && publication.AgentID == agents.reviewer.AgentID, "unexpected publication: %#v", publication)
	if _, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID}); err == nil {
		t.Fatal("duplicate publication was accepted")
	} else {
		requireCode(t, err, CodeConflict)
	}
	if _, err := agents.runtime.CreateAgentCardPublication(ctx, agents.plannerToken, PublishAgentCardInput{AgentID: agents.planner.AgentID}); err == nil {
		t.Fatal("agent credential created a publication")
	} else {
		requireCode(t, err, CodePermissionDenied)
	}
	requireNoError(t, agents.runtime.Close())

	runtimeValue, err := Open(database)
	requireNoError(t, err)
	defer runtimeValue.Close()
	listed, err := runtimeValue.ListAgentCardPublications(ctx, agents.scope.ScopeToken)
	require(t, err == nil && len(listed) == 1 && listed[0].ID == publication.ID && listed[0].Enabled, "unexpected persisted publications: %#v, %v", listed, err)
	summary, err := runtimeValue.StorageSummary(ctx, agents.scope.ScopeToken)
	requireNoError(t, err)
	foundPublication := false
	for _, record := range summary.Records {
		if record.RecordType == "a2aPublication" && record.State == "enabled" && record.Count == 1 {
			foundPublication = true
		}
	}
	require(t, foundPublication, "storage summary omitted publication: %#v", summary)
	disabled, err := runtimeValue.SetAgentCardPublicationEnabled(ctx, agents.scope.ScopeToken, publication.ID, false)
	require(t, err == nil && !disabled.Enabled && disabled.ID == publication.ID, "unexpected disabled publication: %#v, %v", disabled, err)
	if _, _, err := runtimeValue.store.PublishedAgent(ctx, publication.ID); err == nil {
		t.Fatal("disabled publication remained public")
	} else {
		requireCode(t, err, CodeNotFound)
	}
	enabled, err := runtimeValue.SetAgentCardPublicationEnabled(ctx, agents.scope.ScopeToken, publication.ID, true)
	require(t, err == nil && enabled.Enabled && enabled.ID == publication.ID, "unexpected enabled publication: %#v, %v", enabled, err)
	events, err := runtimeValue.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	requireNoError(t, err)
	want := map[string]bool{
		"a2a.publication_created": false, "a2a.publication_disabled": false, "a2a.publication_enabled": false,
	}
	for _, event := range events.Events {
		if _, ok := want[event.Type]; ok && event.SubjectID == publication.ID {
			want[event.Type] = true
		}
	}
	for eventType, found := range want {
		require(t, found, "missing %s event", eventType)
	}
	if _, err := runtimeValue.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: "2999-01-01T00:00:00Z", Execute: true}); err != nil {
		t.Fatal(err)
	}
	listed, err = runtimeValue.ListAgentCardPublications(ctx, agents.scope.ScopeToken)
	require(t, err == nil && len(listed) == 1 && listed[0].Enabled, "retention removed publication: %#v, %v", listed, err)
}

func TestPublishedAgentCardUsesTrustedURLAndHidesPrivateFields(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	server := NewServer(agents.runtime, ServerOptions{PublicBaseURL: "https://bus.example/coordination"})
	path := "/a2a/agents/" + publication.ID + "/.well-known/agent-card.json"
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example"+path, nil)
	request.Host = "attacker.example"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected card response: %d %s", response.Code, response.Body.String())
	}
	var card map[string]any
	requireNoError(t, json.Unmarshal(response.Body.Bytes(), &card))
	interfaces, ok := card["supportedInterfaces"].([]any)
	require(t, ok && len(interfaces) == 1, "unexpected card interfaces: %#v", card)
	interfaceValue, _ := interfaces[0].(map[string]any)
	wantInterface := "https://bus.example/coordination/a2a/agents/" + publication.ID
	if interfaceValue["url"] != wantInterface {
		t.Fatalf("interface URL = %#v, want %q", interfaceValue["url"], wantInterface)
	}
	if response.Header().Get("Cache-Control") != "public, max-age=0" {
		t.Fatalf("unexpected cache policy: %q", response.Header().Get("Cache-Control"))
	}
	encoded := response.Body.String()
	for _, private := range []string{agents.scope.ScopeID, agents.reviewer.AgentID, agents.reviewer.ExecutionID, agents.reviewer.AgentToken, agents.scope.ScopeToken, "attacker.example"} {
		require(t, !strings.Contains(encoded, private), "card exposed private or untrusted value %q: %s", private, encoded)
	}
	if _, err := agents.runtime.RegisterAgent(ctx, agents.scope.ScopeToken, RegisterAgentInput{ID: agents.reviewer.AgentID, DisplayName: "Updated Reviewer"}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, path, nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Updated Reviewer") {
		t.Fatalf("card did not refresh agent metadata: %d %s", response.Code, response.Body.String())
	}
}

func TestDisabledAndUnknownAgentCardsAreIndistinguishable(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	if _, err := agents.runtime.SetAgentCardPublicationEnabled(ctx, agents.scope.ScopeToken, publication.ID, false); err != nil {
		t.Fatal(err)
	}
	server := NewServer(agents.runtime, ServerOptions{PublicBaseURL: "https://bus.example"})
	requestCard := func(id string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/a2a/agents/"+id+"/.well-known/agent-card.json", nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	disabled := requestCard(publication.ID)
	unknown := requestCard("pub_00000000000000000000000000000000")
	if disabled.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || disabled.Body.String() != unknown.Body.String() {
		t.Fatalf("disabled and unknown responses differ: %d %q, %d %q", disabled.Code, disabled.Body.String(), unknown.Code, unknown.Body.String())
	}
}

func TestInvalidPublicBaseURLDoesNotCreatePublication(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	server := NewServer(agents.runtime, ServerOptions{PublicBaseURL: "http://remote.example"})
	body := strings.NewReader(`{"agentId":"reviewer"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/a2a/publications", body)
	request.Header.Set("Authorization", "Bearer "+agents.scope.ScopeToken)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected invalid-base response: %d %s", response.Code, response.Body.String())
	}
	publications, err := agents.runtime.ListAgentCardPublications(context.Background(), agents.scope.ScopeToken)
	require(t, err == nil && len(publications) == 0, "invalid configuration created a publication: %#v, %v", publications, err)
}
