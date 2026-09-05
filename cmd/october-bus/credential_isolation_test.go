package main

import (
	"context"
	"os"
	"testing"

	"github.com/october-dev/october-bus/bus"
)

func TestAuditCredentialChild(t *testing.T) {
	if os.Getenv("BUS_AUDIT_CHILD") != "1" {
		return
	}
	// Report only presence. Never print a credential value.
	for _, name := range []string{"OCTOBER_BUS_ADMIN_TOKEN", "OCTOBER_BUS_SCOPE_TOKEN", "BUS_AUDIT_SCOPE_TOKEN"} {
		if os.Getenv(name) != "" {
			t.Errorf("managed child inherited privileged variable %s", name)
		}
	}
}

func TestAuditManagedChildStripsPrivilegedCredentials(t *testing.T) {
	if os.Getenv("BUS_AUDIT_CHILD") == "1" {
		return
	}
	ctx := context.Background()
	runtime, err := bus.Open(":memory:")
	requireNoError(t, err)
	server := bus.NewServer(runtime, bus.ServerOptions{})
	address, err := server.Start()
	requireNoError(t, err)
	defer server.Stop(ctx)
	scope, err := runtime.CreateScope(ctx, bus.CreateScopeInput{ID: "audit"})
	requireNoError(t, err)
	t.Setenv("BUS_AUDIT_SCOPE_TOKEN", scope.ScopeToken)
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", "synthetic-privileged-scope-marker")
	t.Setenv("OCTOBER_BUS_ADMIN_TOKEN", "synthetic-admin-marker")
	t.Setenv("BUS_AUDIT_CHILD", "1")
	err = runAgent([]string{"--id", "worker", "--name", "Worker", "--address", address, "--scope-token-env", "BUS_AUDIT_SCOPE_TOKEN", "--", os.Args[0], "-test.run=^TestAuditCredentialChild$"})
	require(t, err == nil, "credential isolation check failed: %v", err)
}
