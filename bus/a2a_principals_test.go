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
	if err != nil {
		t.Fatal(err)
	}
	plannerPublication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.planner.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{
		PublicationID: reviewerPublication.ID, Label: "Review service",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Principal.ID == "" || !issued.Principal.Enabled || issued.Principal.PublicationID != reviewerPublication.ID || issued.Credential == "" {
		t.Fatalf("unexpected issued principal: %#v", issued)
	}
	if !strings.HasPrefix(issued.Credential, issued.Principal.ID+".") {
		t.Fatalf("credential does not use its stable principal id: %q", issued.Credential)
	}
	listed, err := agents.runtime.ListA2APrincipals(ctx, agents.scope.ScopeToken)
	if err != nil || len(listed) != 1 || listed[0] != issued.Principal {
		t.Fatalf("unexpected principal list: %#v, %v", listed, err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
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
		if err.Error() != "Invalid scoped credential" {
			t.Fatalf("authentication failure exposed details: %v", err)
		}
	}
	disabled, err := agents.runtime.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("unexpected disabled principal: %#v, %v", disabled, err)
	}
	_, err = agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, reviewerPublication.ID)
	requireCode(t, err, CodeUnauthenticated)
	enabled, err := agents.runtime.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, true)
	if err != nil || !enabled.Enabled {
		t.Fatalf("unexpected enabled principal: %#v, %v", enabled, err)
	}
	if _, err := agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, reviewerPublication.ID); err != nil {
		t.Fatalf("re-enabled credential did not authenticate: %v", err)
	}
	rotated, err := agents.runtime.RotateA2APrincipal(ctx, agents.scope.ScopeToken, issued.Principal.ID)
	if err != nil || rotated.Principal.ID != issued.Principal.ID || rotated.Credential == issued.Credential {
		t.Fatalf("unexpected rotation: %#v, %v", rotated, err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := map[string]bool{
		"credential.created": false, "credential.disabled": false, "credential.enabled": false, "credential.rotated": false,
	}
	for _, event := range events.Events {
		if _, exists := wantEvents[event.Type]; !exists || event.SubjectID != issued.Principal.ID {
			continue
		}
		wantEvents[event.Type] = true
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), issued.Credential) || strings.Contains(string(data), rotated.Credential) || strings.Contains(string(data), issued.Principal.Label) {
			t.Fatalf("credential event exposed private material: %s", data)
		}
	}
	for eventType, found := range wantEvents {
		if !found {
			t.Fatalf("missing %s event", eventType)
		}
	}
}

func TestA2APrincipalAuthorityAndRetention(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Remote caller"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.CreateA2APrincipal(ctx, agents.plannerToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Denied"}); err == nil {
		t.Fatal("agent authority created a remote principal")
	} else {
		requireCode(t, err, CodeUnauthenticated)
	}
	otherScope, err := agents.runtime.CreateScope(ctx, CreateScopeInput{ID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.SetA2APrincipalEnabled(ctx, otherScope.ScopeToken, issued.Principal.ID, false); err == nil {
		t.Fatal("another scope changed a principal")
	} else {
		requireCode(t, err, CodeNotFound)
	}
	summary, err := agents.runtime.StorageSummary(ctx, agents.scope.ScopeToken)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range summary.Records {
		if record.RecordType == "credential" && record.State == "enabled" && record.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("storage summary omitted credential: %#v", summary)
	}
	if _, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: "2999-01-01T00:00:00Z", Execute: true}); err != nil {
		t.Fatal(err)
	}
	listed, err := agents.runtime.ListA2APrincipals(ctx, agents.scope.ScopeToken)
	if err != nil || len(listed) != 1 || listed[0].ID != issued.Principal.ID {
		t.Fatalf("retention removed principal: %#v, %v", listed, err)
	}
}

func TestA2APrincipalPersistsWithoutExposingSecretToBusAPIs(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bus.db")
	agents := setupAgents(t, database)
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Persistent caller"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(agents.runtime, ServerOptions{})
	for _, path := range []string{"/v1/agents", "/mcp"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+issued.Credential)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("scoped credential accessed %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if _, err := agents.runtime.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := agents.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	listed, err := restarted.ListA2APrincipals(ctx, agents.scope.ScopeToken)
	if err != nil || len(listed) != 1 || listed[0].Enabled {
		t.Fatalf("principal status did not survive restart: %#v, %v", listed, err)
	}
	_, err = restarted.AuthenticateA2APrincipal(ctx, issued.Credential, publication.ID)
	requireCode(t, err, CodeUnauthenticated)
	if _, err := restarted.SetA2APrincipalEnabled(ctx, agents.scope.ScopeToken, issued.Principal.ID, true); err != nil {
		t.Fatal(err)
	}
	principal, err := restarted.AuthenticateA2APrincipal(ctx, issued.Credential, publication.ID)
	if err != nil || principal.ID != issued.Principal.ID || principal.Label != issued.Principal.Label {
		t.Fatalf("credential did not survive restart: %#v, %v", principal, err)
	}
}

func TestA2APrincipalHTTPControlsReturnSecretOnlyWhenIssued(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	listed := call(http.MethodGet, "/v1/a2a/principals", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), envelope.Result.Credential) || strings.Contains(listed.Body.String(), `"credential"`) {
		t.Fatalf("unexpected list response: %d %s", listed.Code, listed.Body.String())
	}
	rotated := call(http.MethodPost, "/v1/a2a/principals/"+envelope.Result.Principal.ID+"/rotate", `{}`)
	if rotated.Code != http.StatusOK || !strings.Contains(rotated.Body.String(), `"credential"`) || strings.Contains(rotated.Body.String(), envelope.Result.Credential) {
		t.Fatalf("unexpected rotate response: %d %s", rotated.Code, rotated.Body.String())
	}
}
