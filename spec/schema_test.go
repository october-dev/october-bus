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
		"dependencies": []any{}, "ready": false, "createdAt": "2026-08-30T00:00:00Z", "updatedAt": "2026-08-30T00:00:01Z",
	})
	requireValid(t, task, map[string]any{
		"id": "task_124", "scopeId": "scope", "title": "Plan", "description": "",
		"createdBy": nil, "status": "open", "dependencies": []any{}, "ready": true,
		"createdAt": "2026-08-30T00:00:00Z", "updatedAt": "2026-08-30T00:00:01Z",
	})
	requireInvalid(t, task, map[string]any{
		"id": "task_123", "scopeId": "scope", "title": "Review", "description": "Review the change",
		"createdBy": "planner", "status": "claimed", "dependencies": []any{},
		"ready": false, "createdAt": "2026-08-30T00:00:00Z", "updatedAt": "2026-08-30T00:00:01Z",
	})
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
	planner := bus.Client{Address: address, Token: plannerRegistration.AgentToken}
	reviewer := bus.Client{Address: address, Token: reviewerRegistration.AgentToken}
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
	escalation, err := reviewer.AskHuman(ctx, bus.AskHumanInput{Question: "Proceed?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	requireValid(t, resolvedSchema(t, path, "humanEscalation"), jsonValue(t, escalation))
}
