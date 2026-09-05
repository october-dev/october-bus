package bus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func archiveRequest(t *testing.T, server *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return contractRequest(t, &httpContractFixture{server: server}, method, path, token, body)
}

func TestPortableScopeArchiveAdminRoutes(t *testing.T) {
	ctx := context.Background()
	source := setupAgents(t, ":memory:")
	defer source.runtime.Close()
	server := NewServer(source.runtime, ServerOptions{AdminToken: "source-admin"})

	unauthorized := archiveRequest(t, server, http.MethodGet, "/v1/admin/scopes/test/export", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected unauthorized status: %d", unauthorized.Code)
	}
	active := archiveRequest(t, server, http.MethodGet, "/v1/admin/scopes/test/export", "source-admin", nil)
	if active.Code != http.StatusConflict {
		t.Fatalf("unexpected active export status: %d %s", active.Code, active.Body.String())
	}
	offlineAgents(t, source)
	exported := archiveRequest(t, server, http.MethodGet, "/v1/admin/scopes/test/export", "source-admin", nil)
	if exported.Code != http.StatusOK {
		t.Fatalf("unexpected export status: %d %s", exported.Code, exported.Body.String())
	}
	var exportEnvelope responseEnvelope[ScopeArchive]
	if err := json.NewDecoder(exported.Body).Decode(&exportEnvelope); err != nil || !exportEnvelope.OK {
		t.Fatalf("could not decode export: %#v, %v", exportEnvelope, err)
	}

	targetRuntime, err := Open(":memory:")
	requireNoError(t, err)
	defer targetRuntime.Close()
	target := NewServer(targetRuntime, ServerOptions{AdminToken: "target-admin"})
	imported := archiveRequest(t, target, http.MethodPost, "/v1/admin/scopes/import", "target-admin", exportEnvelope.Result)
	if imported.Code != http.StatusCreated {
		t.Fatalf("unexpected import status: %d %s", imported.Code, imported.Body.String())
	}
	var importEnvelope responseEnvelope[ImportScopeResult]
	if err := json.NewDecoder(imported.Body).Decode(&importEnvelope); err != nil || !importEnvelope.OK || !importEnvelope.Result.Imported || importEnvelope.Result.ScopeToken == "" {
		t.Fatalf("could not decode import: %#v, %v", importEnvelope, err)
	}
	retry := archiveRequest(t, target, http.MethodPost, "/v1/admin/scopes/import", "target-admin", exportEnvelope.Result)
	if retry.Code != http.StatusOK {
		t.Fatalf("unexpected retry status: %d %s", retry.Code, retry.Body.String())
	}
	importEnvelope = responseEnvelope[ImportScopeResult]{}
	if err := json.NewDecoder(retry.Body).Decode(&importEnvelope); err != nil || importEnvelope.Result.Imported || importEnvelope.Result.ScopeToken != "" {
		t.Fatalf("unexpected retry result: %#v, %v", importEnvelope, err)
	}
	if _, err := targetRuntime.ListAgents(ctx, importEnvelope.Result.ScopeToken); err == nil {
		t.Fatal("retry unexpectedly returned reusable authority")
	}
}
