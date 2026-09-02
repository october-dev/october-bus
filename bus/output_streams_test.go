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

func TestOutputStreamsPublishBoundedOrderedValues(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	stream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{
		Name: "site-preview", RetentionLimit: 2, PublisherAgentIDs: []string{agents.reviewer.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stream.ID == "" || stream.Name != "site-preview" || stream.RetentionLimit != 2 || len(stream.PublisherAgentIDs) != 1 {
		t.Fatalf("unexpected stream: %#v", stream)
	}
	_, err = agents.runtime.PublishOutput(ctx, agents.plannerToken, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "denied"})
	requireCode(t, err, CodePermissionDenied)
	values := []PublishOutputInput{
		{ContentType: OutputText, Value: "building", Reference: &OutputReference{URI: "https://example.test/build/1", Title: "Build"}},
		{ContentType: OutputJSON, Value: map[string]any{"status": "ready", "progress": float64(80)}},
		{ContentType: OutputText, Value: "deployed"},
	}
	for index, input := range values {
		value, err := agents.runtime.PublishOutput(ctx, agents.reviewerToken, stream.ID, input)
		if err != nil || value.Sequence != int64(index+1) || value.ProducerType != "agent" || value.ProducerID != agents.reviewer.AgentID {
			t.Fatalf("unexpected output value %d: %#v, %v", index, value, err)
		}
	}
	latest, err := agents.runtime.LatestOutput(ctx, agents.scope.ScopeToken, stream.ID)
	if err != nil || latest == nil || latest.Sequence != 3 || latest.Value != "deployed" {
		t.Fatalf("unexpected latest output: %#v, %v", latest, err)
	}
	history, err := agents.runtime.OutputHistory(ctx, agents.scope.ScopeToken, stream.ID, 1, 10)
	if err != nil || history.ResyncRequired || len(history.Values) != 2 || history.Values[0].Sequence != 2 || history.Values[1].Sequence != 3 {
		t.Fatalf("unexpected retained history: %#v, %v", history, err)
	}
	stale, err := agents.runtime.OutputHistory(ctx, agents.scope.ScopeToken, stream.ID, 0, 10)
	if err != nil || !stale.ResyncRequired || stale.MinimumCursor != 1 || stale.NextSequence != 3 {
		t.Fatalf("unexpected stale history result: %#v, %v", stale, err)
	}
	listed, err := agents.runtime.ListOutputStreams(ctx, agents.scope.ScopeToken)
	if err != nil || len(listed) != 1 || listed[0].CurrentSequence != 3 || listed[0].MinimumCursor != 1 {
		t.Fatalf("unexpected stream list: %#v, %v", listed, err)
	}
	if _, err := agents.runtime.SetOutputPublisher(ctx, agents.scope.ScopeToken, stream.ID, agents.reviewer.AgentID, false); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.PublishOutput(ctx, agents.reviewerToken, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "denied again"})
	requireCode(t, err, CodePermissionDenied)
	updated, err := agents.runtime.SetOutputPublisher(ctx, agents.scope.ScopeToken, stream.ID, agents.planner.AgentID, true)
	if err != nil || len(updated.PublisherAgentIDs) != 1 || updated.PublisherAgentIDs[0] != agents.planner.AgentID {
		t.Fatalf("unexpected publisher update: %#v, %v", updated, err)
	}
	if _, err := agents.runtime.PublishOutput(ctx, agents.plannerToken, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "planner update"}); err != nil {
		t.Fatal(err)
	}
	events, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundPublished := false
	for _, event := range events.Events {
		if event.Type != "output.published" || event.SubjectID != stream.ID {
			continue
		}
		foundPublished = true
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, protected := range []string{"building", "deployed", "planner update", "example.test"} {
			if strings.Contains(string(data), protected) {
				t.Fatalf("output event exposed value content: %s", data)
			}
		}
	}
	if !foundPublished {
		t.Fatal("output publication did not append a scope event")
	}
}

func TestOutputPrincipalsHaveNarrowIndependentPermissions(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	stream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{Name: "build-status"})
	if err != nil {
		t.Fatal(err)
	}
	otherStream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := agents.runtime.CreateOutputPrincipal(ctx, agents.scope.ScopeToken, CreateOutputPrincipalInput{
		StreamID: stream.ID, Label: "Dashboard", Permissions: []OutputPermission{OutputRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := agents.runtime.CreateOutputPrincipal(ctx, agents.scope.ScopeToken, CreateOutputPrincipalInput{
		StreamID: stream.ID, Label: "Build service", Permissions: []OutputPermission{OutputPublish},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.PublishOutput(ctx, reader.Credential, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "denied"})
	requireCode(t, err, CodeUnauthenticated)
	value, err := agents.runtime.PublishOutput(ctx, publisher.Credential, stream.ID, PublishOutputInput{
		ContentType: OutputJSON, Value: map[string]any{"status": "ready"},
	})
	if err != nil || value.ProducerType != "principal" || value.ProducerID != publisher.Principal.ID {
		t.Fatalf("unexpected principal publication: %#v, %v", value, err)
	}
	_, err = agents.runtime.LatestOutput(ctx, publisher.Credential, stream.ID)
	requireCode(t, err, CodeUnauthenticated)
	latest, err := agents.runtime.LatestOutput(ctx, reader.Credential, stream.ID)
	if err != nil || latest == nil || latest.Sequence != 1 {
		t.Fatalf("reader could not read its stream: %#v, %v", latest, err)
	}
	_, err = agents.runtime.LatestOutput(ctx, reader.Credential, otherStream.ID)
	requireCode(t, err, CodeUnauthenticated)
	_, err = agents.runtime.ListAgents(ctx, reader.Credential)
	requireCode(t, err, CodeUnauthenticated)
	principals, err := agents.runtime.ListOutputPrincipals(ctx, agents.scope.ScopeToken)
	if err != nil || len(principals) != 2 {
		t.Fatalf("unexpected output principals: %#v, %v", principals, err)
	}
	data, err := json.Marshal(principals)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), reader.Credential) || strings.Contains(string(data), publisher.Credential) || strings.Contains(string(data), `"credential"`) {
		t.Fatalf("principal list exposed a credential: %s", data)
	}
	if _, err := agents.runtime.SetOutputPrincipalEnabled(ctx, agents.scope.ScopeToken, reader.Principal.ID, false); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.LatestOutput(ctx, reader.Credential, stream.ID)
	requireCode(t, err, CodeUnauthenticated)
	rotated, err := agents.runtime.RotateOutputPrincipal(ctx, agents.scope.ScopeToken, reader.Principal.ID)
	if err != nil || rotated.Credential == reader.Credential || rotated.Principal.Enabled {
		t.Fatalf("unexpected principal rotation: %#v, %v", rotated, err)
	}
	if _, err := agents.runtime.SetOutputPrincipalEnabled(ctx, agents.scope.ScopeToken, reader.Principal.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.LatestOutput(ctx, reader.Credential, stream.ID)
	requireCode(t, err, CodeUnauthenticated)
	if _, err := agents.runtime.LatestOutput(ctx, rotated.Credential, stream.ID); err != nil {
		t.Fatalf("rotated credential did not read: %v", err)
	}
}

func TestOutputStreamsPersistAndRemoveTheirDataAndCredentials(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bus.db")
	agents := setupAgents(t, database)
	ctx := context.Background()
	stream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{
		Name: "preview", PublisherAgentIDs: []string{agents.reviewer.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := agents.runtime.CreateOutputPrincipal(ctx, agents.scope.ScopeToken, CreateOutputPrincipalInput{
		StreamID: stream.ID, Label: "Preview page", Permissions: []OutputPermission{OutputRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.PublishOutput(ctx, agents.reviewerToken, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "ready"}); err != nil {
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
	latest, err := restarted.LatestOutput(ctx, reader.Credential, stream.ID)
	if err != nil || latest == nil || latest.Value != "ready" {
		t.Fatalf("output did not survive restart: %#v, %v", latest, err)
	}
	if err := restarted.RemoveOutputStream(ctx, agents.scope.ScopeToken, stream.ID); err != nil {
		t.Fatal(err)
	}
	_, err = restarted.LatestOutput(ctx, reader.Credential, stream.ID)
	requireCode(t, err, CodeUnauthenticated)
	principals, err := restarted.ListOutputPrincipals(ctx, agents.scope.ScopeToken)
	if err != nil || len(principals) != 0 {
		t.Fatalf("stream removal left principals: %#v, %v", principals, err)
	}
	var valueCount int
	if err := sqliteStore(t, restarted).db.QueryRow(`SELECT COUNT(*) FROM output_values WHERE stream_id=?`, stream.ID).Scan(&valueCount); err != nil || valueCount != 0 {
		t.Fatalf("stream removal left values: %d, %v", valueCount, err)
	}
}

func TestOutputPayloadLimitsAndPerPrincipalQuota(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	stream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{
		Name: "limited", PublisherAgentIDs: []string{agents.reviewer.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []PublishOutputInput{
		{ContentType: OutputText, Value: map[string]any{"not": "text"}},
		{ContentType: OutputContentType("image/png"), Value: "no"},
		{ContentType: OutputText, Value: strings.Repeat("x", maxOutputTextBytes+1)},
		{ContentType: OutputJSON, Value: strings.Repeat("x", maxOutputJSONBytes+1)},
		{ContentType: OutputText, Value: "bad reference", Reference: &OutputReference{URI: "/relative"}},
	} {
		_, err := agents.runtime.PublishOutput(ctx, agents.reviewerToken, stream.ID, input)
		requireCode(t, err, CodeInvalidArgument)
	}
	window := nowMillis()
	window -= window % 60000
	if _, err := sqliteStore(t, agents.runtime).db.Exec(`INSERT INTO output_rate_usage(scope_id,principal_type,principal_id,window_start,publish_count) VALUES(?,?,?,?,?),(?,?,?,?,?)`,
		agents.scope.ScopeID, "agent", agents.reviewer.AgentID, window, outputPublishRate,
		agents.scope.ScopeID, "agent", agents.reviewer.AgentID, window+60000, outputPublishRate); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.PublishOutput(ctx, agents.reviewerToken, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "over limit"})
	requireCode(t, err, CodeBackpressure)
	reader, err := agents.runtime.CreateOutputPrincipal(ctx, agents.scope.ScopeToken, CreateOutputPrincipalInput{
		StreamID: stream.ID, Label: "Limited reader", Permissions: []OutputPermission{OutputRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqliteStore(t, agents.runtime).db.Exec(`INSERT INTO output_rate_usage(scope_id,principal_type,principal_id,window_start,read_count) VALUES(?,?,?,?,?),(?,?,?,?,?)`,
		agents.scope.ScopeID, "principal", reader.Principal.ID, window, outputReadRate,
		agents.scope.ScopeID, "principal", reader.Principal.ID, window+60000, outputReadRate); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.LatestOutput(ctx, reader.Credential, stream.ID)
	requireCode(t, err, CodeBackpressure)
}

func TestOutputRoutesRequireExplicitBrowserOrigin(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	stream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{
		Name: "browser", PublisherAgentIDs: []string{agents.reviewer.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := agents.runtime.CreateOutputPrincipal(ctx, agents.scope.ScopeToken, CreateOutputPrincipalInput{
		StreamID: stream.ID, Label: "Browser", Permissions: []OutputPermission{OutputRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.PublishOutput(ctx, agents.reviewerToken, stream.ID, PublishOutputInput{ContentType: OutputText, Value: "ready"}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(agents.runtime, ServerOptions{AllowedOrigins: []string{"https://dashboard.example"}})
	call := func(method, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/outputs/"+stream.ID+"/latest", nil)
		request.Header.Set("Origin", origin)
		if method != http.MethodOptions {
			request.Header.Set("Authorization", "Bearer "+reader.Credential)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	allowed := call(http.MethodGet, "https://dashboard.example")
	if allowed.Code != http.StatusOK || allowed.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example" {
		t.Fatalf("allowed origin failed: %d %#v", allowed.Code, allowed.Header())
	}
	preflight := call(http.MethodOptions, "https://dashboard.example")
	if preflight.Code != http.StatusNoContent || !strings.Contains(preflight.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("unexpected preflight: %d %#v", preflight.Code, preflight.Header())
	}
	denied := call(http.MethodGet, "https://attacker.example")
	if denied.Code != http.StatusForbidden || denied.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected denied origin: %d %#v", denied.Code, denied.Header())
	}
	serverToServer := call(http.MethodGet, "")
	if serverToServer.Code != http.StatusOK || serverToServer.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("server request required browser configuration: %d %#v", serverToServer.Code, serverToServer.Header())
	}
	request := httptest.NewRequest(http.MethodGet, "/outputs/"+stream.ID+"/latest?credential="+reader.Credential, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("query-string credential was accepted: %d %s", response.Code, response.Body.String())
	}
}
