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

func TestA2APrincipalLifecycleAndIsolation(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	reviewerPublication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	plannerPublication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.planner.AgentID})
	requireNoError(t, err)
	issued, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{
		PublicationID: reviewerPublication.ID, Label: "Review service",
	})
	requireNoError(t, err)
	require(t, issued.Principal.ID != "" && issued.Principal.Enabled && issued.Principal.PublicationID == reviewerPublication.ID && issued.Credential != "", "unexpected issued principal: %#v", issued)
	if !strings.HasPrefix(issued.Credential, issued.Principal.ID+".") {
		t.Fatalf("credential does not use its stable principal id: %q", issued.Credential)
	}
	listed, err := agents.runtime.ListA2APrincipals(ctx, agents.scope.ScopeToken)
	require(t, err == nil && len(listed) == 1 && listed[0] == issued.Principal, "unexpected principal list: %#v, %v", listed, err)
	encoded, err := json.Marshal(listed)
	requireNoError(t, err)
	if strings.Contains(string(encoded), issued.Credential) {
		t.Fatal("principal list exposed a credential")
	}
	if _, err := agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, reviewerPublication.ID); err != nil {
		t.Fatalf("issued credential did not authenticate: %v", err)
	}
	for _, attempt := range []struct {
		credential    string
		publicationID string
	}{
		{issued.Credential, plannerPublication.ID},
		{issued.Credential + "x", reviewerPublication.ID},
		{"cred_unknown.invalid", reviewerPublication.ID},
	} {
		_, err := agents.runtime.AuthenticateA2APrincipal(ctx, attempt.credential, attempt.publicationID)
		requireCode(t, err, CodeUnauthenticated)
		require(t, err.Error() == "Invalid scoped credential", "authentication failure exposed details: %v", err)
	}
	disabled, err := agents.runtime.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, false)
	require(t, err == nil && !disabled.Enabled, "unexpected disabled principal: %#v, %v", disabled, err)
	_, err = agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, reviewerPublication.ID)
	requireCode(t, err, CodeUnauthenticated)
	enabled, err := agents.runtime.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, true)
	require(t, err == nil && enabled.Enabled, "unexpected enabled principal: %#v, %v", enabled, err)
	if _, err := agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, reviewerPublication.ID); err != nil {
		t.Fatalf("re-enabled credential did not authenticate: %v", err)
	}
	rotated, err := agents.runtime.RotateA2APrincipal(ctx, agents.scope.ScopeToken, issued.Principal.ID)
	require(t, err == nil && rotated.Principal.ID == issued.Principal.ID && rotated.Credential != issued.Credential, "unexpected rotation: %#v, %v", rotated, err)
	_, err = agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, reviewerPublication.ID)
	requireCode(t, err, CodeUnauthenticated)
	if _, err := agents.runtime.AuthenticateA2APrincipal(ctx, rotated.Credential, reviewerPublication.ID); err != nil {
		t.Fatalf("rotated credential did not authenticate: %v", err)
	}
	if _, err := agents.runtime.SetAgentCardPublicationEnabled(ctx, agents.scope.ScopeToken, reviewerPublication.ID, false); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.AuthenticateA2APrincipal(ctx, rotated.Credential, reviewerPublication.ID)
	requireCode(t, err, CodeUnauthenticated)

	events, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	requireNoError(t, err)
	wantEvents := map[string]bool{
		"credential.created": false, "credential.disabled": false, "credential.enabled": false, "credential.rotated": false,
	}
	for _, event := range events.Events {
		if _, exists := wantEvents[event.Type]; !exists || event.SubjectID != issued.Principal.ID {
			continue
		}
		wantEvents[event.Type] = true
		data, err := json.Marshal(event)
		requireNoError(t, err)
		require(t, !strings.Contains(string(data), issued.Credential) && !strings.Contains(string(data), rotated.Credential) && !strings.Contains(string(data), issued.Principal.Label), "credential event exposed private material: %s", data)
	}
	for eventType, found := range wantEvents {
		require(t, found, "missing %s event", eventType)
	}
}

func TestA2APrincipalAuthorityAndRetention(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	issued, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Remote caller"})
	requireNoError(t, err)
	if _, err := agents.runtime.CreateA2APrincipal(ctx, agents.plannerToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Denied"}); err == nil {
		t.Fatal("agent authority created a remote principal")
	} else {
		requireCode(t, err, CodePermissionDenied)
	}
	otherScope, err := agents.runtime.CreateScope(ctx, CreateScopeInput{ID: "other"})
	requireNoError(t, err)
	if _, err := agents.runtime.SetA2APrincipalEnabled(ctx, otherScope.ScopeToken, issued.Principal.ID, false); err == nil {
		t.Fatal("another scope changed a principal")
	} else {
		requireCode(t, err, CodeNotFound)
	}
	summary, err := agents.runtime.StorageSummary(ctx, agents.scope.ScopeToken)
	requireNoError(t, err)
	found := false
	for _, record := range summary.Records {
		if record.RecordType == "credential" && record.State == "enabled" && record.Count == 1 {
			found = true
		}
	}
	require(t, found, "storage summary omitted credential: %#v", summary)
	if _, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: "2999-01-01T00:00:00Z", Execute: true}); err != nil {
		t.Fatal(err)
	}
	listed, err := agents.runtime.ListA2APrincipals(ctx, agents.scope.ScopeToken)
	require(t, err == nil && len(listed) == 1 && listed[0].ID == issued.Principal.ID, "retention removed principal: %#v, %v", listed, err)
}

func TestA2APrincipalPersistsWithoutExposingSecretToBusAPIs(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bus.db")
	agents := setupAgents(t, database)
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	issued, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Persistent caller"})
	requireNoError(t, err)
	server := NewServer(agents.runtime, ServerOptions{})
	for path, want := range map[string]int{"/v1/agents": http.StatusForbidden, "/mcp": http.StatusUnauthorized} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+issued.Credential)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("scoped credential accessed %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if _, err := agents.runtime.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, false); err != nil {
		t.Fatal(err)
	}
	requireNoError(t, agents.runtime.Close())
	restarted, err := Open(database)
	requireNoError(t, err)
	defer restarted.Close()
	listed, err := restarted.ListA2APrincipals(ctx, agents.scope.ScopeToken)
	require(t, err == nil && len(listed) == 1 && !listed[0].Enabled, "principal status did not survive restart: %#v, %v", listed, err)
	_, err = restarted.AuthenticateA2APrincipal(ctx, issued.Credential, publication.ID)
	requireCode(t, err, CodeUnauthenticated)
	if _, err := restarted.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, true); err != nil {
		t.Fatal(err)
	}
	principal, err := restarted.AuthenticateA2APrincipal(ctx, issued.Credential, publication.ID)
	require(t, err == nil && principal.ID == issued.Principal.ID && principal.Label == issued.Principal.Label, "credential did not survive restart: %#v, %v", principal, err)
}

func TestA2APrincipalHTTPControlsReturnSecretOnlyWhenIssued(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	requireNoError(t, err)
	server := NewServer(agents.runtime, ServerOptions{})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+agents.scope.ScopeToken)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	created := call(http.MethodPost, "/v1/a2a/principals", `{"publicationId":"`+publication.ID+`","label":"Web client"}`)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"credential"`) {
		t.Fatalf("unexpected create response: %d %s", created.Code, created.Body.String())
	}
	var envelope struct {
		Result IssuedA2APrincipal `json:"result"`
	}
	requireNoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	listed := call(http.MethodGet, "/v1/a2a/principals", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), envelope.Result.Credential) || strings.Contains(listed.Body.String(), `"credential"`) {
		t.Fatalf("unexpected list response: %d %s", listed.Code, listed.Body.String())
	}
	rotated := call(http.MethodPost, "/v1/a2a/principals/"+envelope.Result.Principal.ID+"/rotate", `{}`)
	if rotated.Code != http.StatusOK || !strings.Contains(rotated.Body.String(), `"credential"`) || strings.Contains(rotated.Body.String(), envelope.Result.Credential) {
		t.Fatalf("unexpected rotate response: %d %s", rotated.Code, rotated.Body.String())
	}
}
