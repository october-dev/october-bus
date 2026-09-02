package spec_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/october-dev/october-bus/bus"
)

func TestCodexAdapterForwardsOnlyExecutionCredentials(t *testing.T) {
	configuration, err := os.ReadFile(filepath.Join("..", "adapters", "codex", "config.toml.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(configuration)
	if !strings.Contains(text, `env_vars = ["OCTOBER_BUS_ADDRESS", "OCTOBER_BUS_AGENT_TOKEN"]`) {
		t.Fatal("Codex adapter must forward the Bus address and execution token")
	}
	if strings.Contains(text, "OCTOBER_BUS_SCOPE_TOKEN") || strings.Contains(text, "OCTOBER_BUS_ADMIN_TOKEN") {
		t.Fatal("Codex adapter must not forward scope or admin credentials")
	}
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func resolvedSchema(t *testing.T, path, ref string) *jsonschema.Resolved {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if ref != "" {
		schema.Ref = "#/$defs/" + ref
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func requireValid(t *testing.T, schema *jsonschema.Resolved, value any) {
	t.Helper()
	if err := schema.Validate(value); err != nil {
		t.Fatalf("expected valid value: %v", err)
	}
}

func requireInvalid(t *testing.T, schema *jsonschema.Resolved, value any) {
	t.Helper()
	if err := schema.Validate(value); err == nil {
		t.Fatal("expected schema validation failure")
	}
}

func jsonValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProtocolSchemas(t *testing.T) {
	path := filepath.Join("0.1", "schemas", "protocol.schema.json")
	register := resolvedSchema(t, path, "registerAgentInput")
	requireValid(t, register, map[string]any{
		"id": "reviewer", "displayName": "Reviewer", "leaseMs": float64(30000),
		"capabilities": []any{map[string]any{"name": "review"}},
	})
	requireInvalid(t, register, map[string]any{"id": "bad id", "displayName": "Reviewer"})
	requireInvalid(t, register, map[string]any{"displayName": "Reviewer", "unknown": true})

	send := resolvedSchema(t, path, "sendMessageInput")
	requireValid(t, send, map[string]any{
		"to": "reviewer", "body": "Review this", "mode": "request",
		"idempotencyKey": "4e3de56b-8f21-40b4-9e94-cd29185850ce",
	})
	requireValid(t, send, map[string]any{
		"to": "planner", "body": "Found an issue", "mode": "response", "responseTo": "msg_123",
	})
	requireInvalid(t, send, map[string]any{
		"to": "planner", "body": "Missing correlation", "mode": "response",
	})
	requireInvalid(t, send, map[string]any{
		"to": "planner", "body": "Unexpected correlation", "mode": "notify", "responseTo": "msg_123",
	})

	reserve := resolvedSchema(t, path, "reserveInboxInput")
	requireValid(t, reserve, map[string]any{"limit": float64(50), "waitMs": float64(25000)})
	requireValid(t, reserve, map[string]any{})
	requireValid(t, reserve, map[string]any{"limit": float64(0), "waitMs": float64(0)})
	requireInvalid(t, reserve, map[string]any{"waitMs": float64(25001)})
	requireInvalid(t, reserve, map[string]any{"waitMs": float64(1.5)})

	task := resolvedSchema(t, path, "task")
	requireValid(t, task, map[string]any{
		"id": "task_123", "scopeId": "scope", "title": "Review", "description": "Review the change",
		"createdBy": "planner", "claimedBy": "reviewer", "status": "done",
		"dependencies": []any{}, "ready": false, "recentProgress": []any{},
		"createdAt": "2026-08-30T00:00:00Z", "updatedAt": "2026-08-30T00:00:01Z",
	})
	requireValid(t, task, map[string]any{
		"id": "task_124", "scopeId": "scope", "title": "Plan", "description": "",
		"createdBy": nil, "status": "open", "dependencies": []any{}, "ready": true, "recentProgress": []any{},
		"createdAt": "2026-08-30T00:00:00Z", "updatedAt": "2026-08-30T00:00:01Z",
	})
	requireInvalid(t, task, map[string]any{
		"id": "task_123", "scopeId": "scope", "title": "Review", "description": "Review the change",
		"createdBy": "planner", "status": "claimed", "dependencies": []any{},
		"ready": false, "recentProgress": []any{},
		"createdAt": "2026-08-30T00:00:00Z", "updatedAt": "2026-08-30T00:00:01Z",
	})
	progress := resolvedSchema(t, path, "taskProgress")
	requireValid(t, progress, map[string]any{
		"taskId": "task_123", "sequence": float64(1), "agentId": "reviewer", "executionId": "exec_123",
		"kind": "blocker", "text": "Waiting for an API decision", "createdAt": "2026-08-30T00:00:01Z",
	})
	events := resolvedSchema(t, path, "eventBatch")
	requireValid(t, events, map[string]any{
		"scopeId": "scope", "events": []any{map[string]any{
			"id": "evt_123", "scopeId": "scope", "type": "task.created", "subjectId": "task_123",
			"revision": float64(1), "attributes": map[string]any{"status": "open"}, "createdAt": "2026-08-30T00:00:00Z",
		}},
		"nextRevision": float64(1), "currentRevision": float64(1), "minimumCursor": float64(0), "resyncRequired": false,
	})
	publication := resolvedSchema(t, path, "agentCardPublication")
	requireValid(t, publication, map[string]any{
		"id": "pub_123", "scopeId": "scope", "agentId": "reviewer", "enabled": true,
		"cardUrl":      "https://bus.example/a2a/agents/pub_123/.well-known/agent-card.json",
		"interfaceUrl": "https://bus.example/a2a/agents/pub_123",
		"createdAt":    "2026-09-02T00:00:00Z", "updatedAt": "2026-09-02T00:00:00Z",
	})
	publishInput := resolvedSchema(t, path, "publishAgentCardInput")
	requireValid(t, publishInput, map[string]any{"agentId": "reviewer"})
	requireInvalid(t, publishInput, map[string]any{"agentId": "reviewer", "scopeId": "scope"})
	principal := resolvedSchema(t, path, "a2aPrincipal")
	requireValid(t, principal, map[string]any{
		"id": "cred_123", "scopeId": "scope", "publicationId": "pub_123", "label": "Review service", "enabled": true,
		"createdAt": "2026-09-02T00:00:00Z", "updatedAt": "2026-09-02T00:00:00Z",
	})
	issuedPrincipal := resolvedSchema(t, path, "issuedA2APrincipal")
	requireValid(t, issuedPrincipal, map[string]any{
		"principal": map[string]any{
			"id": "cred_123", "scopeId": "scope", "publicationId": "pub_123", "label": "Review service", "enabled": true,
			"createdAt": "2026-09-02T00:00:00Z", "updatedAt": "2026-09-02T00:00:00Z",
		},
		"credential": "cred_123.secret",
	})
	createPrincipal := resolvedSchema(t, path, "createA2APrincipalInput")
	requireValid(t, createPrincipal, map[string]any{"publicationId": "pub_123", "label": "Review service"})
	requireInvalid(t, createPrincipal, map[string]any{"publicationId": "pub_123", "label": ""})
	outputStream := resolvedSchema(t, path, "outputStream")
	requireValid(t, outputStream, map[string]any{
		"id": "out_123", "scopeId": "scope", "name": "site-preview", "retentionLimit": float64(1000),
		"currentSequence": float64(1), "minimumCursor": float64(0), "publisherAgentIds": []any{"reviewer"},
		"createdAt": "2026-09-02T00:00:00Z", "updatedAt": "2026-09-02T00:00:01Z",
	})
	publishOutput := resolvedSchema(t, path, "publishOutputInput")
	requireValid(t, publishOutput, map[string]any{
		"contentType": "application/json", "value": map[string]any{"status": "ready"},
		"reference": map[string]any{"uri": "https://example.test/preview", "title": "Preview"},
	})
	requireValid(t, publishOutput, map[string]any{"contentType": "text/plain", "value": "building"})
	requireInvalid(t, publishOutput, map[string]any{"contentType": "text/plain", "value": map[string]any{"status": "wrong"}})
	outputHistory := resolvedSchema(t, path, "outputHistory")
	requireValid(t, outputHistory, map[string]any{
		"streamId": "out_123", "values": []any{map[string]any{
			"streamId": "out_123", "sequence": float64(1), "producerType": "agent", "producerId": "reviewer",
			"contentType": "text/plain", "value": "ready", "createdAt": "2026-09-02T00:00:01Z",
		}},
		"nextSequence": float64(1), "currentSequence": float64(1), "minimumCursor": float64(0), "resyncRequired": false,
	})
	outputPrincipal := resolvedSchema(t, path, "issuedOutputPrincipal")
	requireValid(t, outputPrincipal, map[string]any{
		"principal": map[string]any{
			"id": "cred_456", "scopeId": "scope", "streamId": "out_123", "label": "Dashboard",
			"permissions": []any{"read"}, "enabled": true,
			"createdAt": "2026-09-02T00:00:00Z", "updatedAt": "2026-09-02T00:00:00Z",
		},
		"credential": "cred_456.secret",
	})

	prune := resolvedSchema(t, path, "pruneScopeInput")
	requireValid(t, prune, map[string]any{"before": "2026-08-01T00:00:00Z"})
	requireValid(t, prune, map[string]any{"before": "2026-08-01T00:00:00Z", "execute": true})
}

func TestPortableScopeArchiveSchema(t *testing.T) {
	schema := resolvedSchema(t, filepath.Join("0.1", "schemas", "scope-archive.schema.json"), "")
	archive := bus.ScopeArchive{
		Format: bus.ScopeArchiveFormat, Version: bus.ScopeArchiveVersion,
		ExportedAt: "2026-09-02T00:00:00Z",
		Scope:      bus.ArchivedScope{ID: "project", CreatedAt: "2026-09-01T00:00:00Z"},
		Agents: []bus.ArchivedAgent{{
			ID: "reviewer", DisplayName: "Reviewer", Capabilities: []bus.AgentCapability{{Name: "review"}},
			RegisteredAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:01Z",
		}},
		Links:                 []bus.ArchivedPeerLink{},
		Messages:              []bus.ArchivedMessage{},
		Tasks:                 []bus.ArchivedTask{},
		TaskProgress:          []bus.ArchivedTaskProgress{},
		Escalations:           []bus.ArchivedEscalation{},
		AgentCardPublications: []bus.ArchivedAgentCard{},
		OutputStreams:         []bus.ArchivedOutputStream{},
		OutputValues:          []bus.ArchivedOutputValue{},
	}
	value := jsonValue(t, archive)
	requireValid(t, schema, value)
	object := value.(map[string]any)
	object["scopeToken"] = "must-not-be-portable"
	requireInvalid(t, schema, object)
}

func TestAdapterManifestsMatchSchema(t *testing.T) {
	schema := resolvedSchema(t, filepath.Join("0.1", "schemas", "adapter-manifest.schema.json"), "")
	paths, err := filepath.Glob(filepath.Join("..", "adapters", "*", "adapter.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no adapter manifests found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			requireValid(t, schema, readJSON(t, path))
		})
	}
	requireInvalid(t, schema, map[string]any{
		"schemaVersion": float64(1), "id": "unproven", "harnessFamily": "Unknown",
		"adapterVersion": "0.1.0",
		"status":         "verified", "protocolVersions": []any{"0.1"}, "transport": "mcp",
		"delivery": "pull", "lifecycleEvidence": "process", "capabilities": []any{},
		"testedVersions": []any{}, "platforms": []any{"linux"}, "maintainer": "October",
		"repository": "https://github.com/october-dev/october-bus",
	})
}

func TestCompatibilityEvidenceSchema(t *testing.T) {
	schema := resolvedSchema(t, filepath.Join("0.1", "schemas", "compatibility-evidence.schema.json"), "")
	evidence := map[string]any{
		"schemaVersion": float64(1), "harnessFamily": "Example", "harnessVersion": "1.0.0",
		"adapterId": "example-mcp", "adapterVersion": "0.1.0", "protocolVersion": "0.1",
		"runtimeVersion": "0.1.0", "operatingSystem": "linux", "architecture": "amd64",
		"profile": "mcp-adapter", "result": "passed",
		"resultDigest":     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"verifiedAt":       "2026-08-30T00:00:00Z",
		"repositoryCommit": "0123456789abcdef0123456789abcdef01234567",
		"verificationMode": "automated", "limitations": []any{},
	}
	requireValid(t, schema, evidence)
	evidence["resultDigest"] = "unverified"
	requireInvalid(t, schema, evidence)
}

func TestCompatibilityRegistrySchema(t *testing.T) {
	schema := resolvedSchema(t, filepath.Join("0.1", "schemas", "compatibility-registry.schema.json"), "")
	repositoryRoot := filepath.Join("..")
	registry := readJSON(t, filepath.Join(repositoryRoot, "compatibility", "registry.json"))
	requireValid(t, schema, registry)

	value, ok := registry.(map[string]any)
	if !ok {
		t.Fatal("compatibility registry must be an object")
	}
	paths, ok := value["verified"].([]any)
	if !ok {
		t.Fatal("compatibility registry verified field must be an array")
	}
	evidenceSchema := resolvedSchema(t, filepath.Join("0.1", "schemas", "compatibility-evidence.schema.json"), "")
	for _, entry := range paths {
		path, ok := entry.(string)
		if !ok {
			t.Fatal("compatibility registry entries must be strings")
		}
		requireValid(t, evidenceSchema, readJSON(t, filepath.Join(repositoryRoot, "compatibility", path)))
	}
}

func TestReferenceRuntimeResponsesMatchProtocolSchemas(t *testing.T) {
	ctx := context.Background()
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{AdminToken: "schema-admin-token"})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	client := bus.Client{Address: address, Token: "schema-admin-token"}
	path := filepath.Join("0.1", "schemas", "protocol.schema.json")

	health, err := client.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "health"), jsonValue(t, health))
	scope, err := client.CreateScope(ctx, bus.CreateScopeInput{ID: "schema"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "createScopeResult"), jsonValue(t, scope))
	owner := bus.Client{Address: address, Token: scope.ScopeToken}
	plannerRegistration, err := owner.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "planner", DisplayName: "Planner"})
	if err != nil {
		t.Fatal(err)
	}
	reviewerRegistration, err := owner.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "reviewer", DisplayName: "Reviewer", ConnectTo: []string{"planner"}})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "registerAgentResult"), jsonValue(t, reviewerRegistration))
	publication, err := owner.CreateAgentCardPublication(ctx, bus.PublishAgentCardInput{AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "agentCardPublication"), jsonValue(t, publication))
	principal, err := owner.CreateA2APrincipal(ctx, bus.CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Review service"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "issuedA2APrincipal"), jsonValue(t, principal))
	outputStream, err := owner.CreateOutputStream(ctx, bus.CreateOutputStreamInput{
		Name: "site-preview", PublisherAgentIDs: []string{"reviewer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "outputStream"), jsonValue(t, outputStream))
	outputPrincipal, err := owner.CreateOutputPrincipal(ctx, bus.CreateOutputPrincipalInput{
		StreamID: outputStream.ID, Label: "Dashboard", Permissions: []bus.OutputPermission{bus.OutputRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "issuedOutputPrincipal"), jsonValue(t, outputPrincipal))
	planner := bus.Client{Address: address, Token: plannerRegistration.AgentToken}
	reviewer := bus.Client{Address: address, Token: reviewerRegistration.AgentToken}
	output, err := reviewer.PublishOutput(ctx, outputStream.ID, bus.PublishOutputInput{ContentType: bus.OutputText, Value: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "outputValue"), jsonValue(t, output))
	history, err := (bus.Client{Address: address, Token: outputPrincipal.Credential}).OutputHistory(ctx, outputStream.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "outputHistory"), jsonValue(t, history))
	agent, err := planner.Heartbeat(ctx, bus.HeartbeatInput{Lifecycle: bus.LifecycleReady, Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "agent"), jsonValue(t, agent))
	receipt, err := planner.SendMessage(ctx, bus.SendMessageInput{To: "reviewer", Body: "Review", Mode: bus.MessageRequest})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "deliveryReceipt"), jsonValue(t, receipt))
	messages, err := reviewer.PullInbox(ctx, 10, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("unexpected messages: %#v, %v", messages, err)
	}
	requireValid(t, resolvedSchema(t, path, "message"), jsonValue(t, messages[0]))
	task, err := planner.AddTask(ctx, bus.AddTaskInput{Title: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "task"), jsonValue(t, task))
	if _, err := planner.ClaimTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	progressEntry, err := planner.AddTaskProgress(ctx, task.ID, bus.AddTaskProgressInput{Kind: "progress", Text: "Review started"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "taskProgress"), jsonValue(t, progressEntry))
	progressHistory, err := owner.ListTaskProgress(ctx, task.ID)
	if err != nil || len(progressHistory) != 1 {
		t.Fatalf("unexpected progress history: %#v, %v", progressHistory, err)
	}
	events, err := owner.Events(ctx, 0, 100, 0)
	if err != nil || len(events.Events) == 0 {
		t.Fatalf("unexpected scope events: %#v, %v", events, err)
	}
	requireValid(t, resolvedSchema(t, path, "eventBatch"), jsonValue(t, events))
	storage, err := owner.StorageSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "storageSummary"), jsonValue(t, storage))
	pruneResult, err := owner.PruneScope(ctx, bus.PruneScopeInput{Before: "2026-08-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "pruneScopeResult"), jsonValue(t, pruneResult))
	escalation, err := reviewer.AskHuman(ctx, bus.AskHumanInput{Question: "Proceed?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "humanEscalation"), jsonValue(t, escalation))
}
