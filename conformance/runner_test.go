package conformance_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/october-dev/october-bus/bus"
	"github.com/october-dev/october-bus/conformance"
)

func buildRuntimeCommand(t *testing.T) string {
	t.Helper()
	name := "october-bus"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", path, "../cmd/october-bus")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build runtime command: %v\n%s", err, output)
	}
	return path
}

func TestLocalRuntimeProfile(t *testing.T) {
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{AdminToken: "conformance-admin-token"})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := conformance.Run(ctx, conformance.Options{Address: address, AdminToken: "conformance-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile != conformance.ProfileLocalRuntime || result.ProtocolVersion != bus.ProtocolVersion || len(result.Passed) < 10 || len(result.Failed) != 0 || result.CompletedAt == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestFailureResultNamesTheFailedCheck(t *testing.T) {
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{AdminToken: "correct-token"})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	result, err := conformance.Run(context.Background(), conformance.Options{Address: address, AdminToken: "wrong-token"})
	if err == nil || len(result.Failed) != 1 || result.Failed[0].Check != "scope-authority" || result.CompletedAt == "" {
		t.Fatalf("unexpected failure result: %#v, %v", result, err)
	}
}

func TestMCPAdapterProfile(t *testing.T) {
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{AdminToken: "conformance-admin-token"})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	binary := buildRuntimeCommand(t)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	result, err := conformance.RunMCPAdapter(ctx, conformance.MCPAdapterOptions{
		Address: address, AdminToken: "conformance-admin-token", Command: binary, Args: []string{"mcp", "stdio"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile != conformance.ProfileMCPAdapter || result.ProtocolVersion != bus.ProtocolVersion || len(result.Passed) != 13 || len(result.Failed) != 0 || result.CompletedAt == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestMCPAdapterFailureNamesStartupCheck(t *testing.T) {
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{AdminToken: "conformance-admin-token"})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	result, err := conformance.RunMCPAdapter(context.Background(), conformance.MCPAdapterOptions{
		Address: address, AdminToken: "conformance-admin-token", Command: buildRuntimeCommand(t), Args: []string{"version"},
	})
	if err == nil || len(result.Failed) != 1 || result.Failed[0].Check != "adapter-start-and-execution-identity" || result.CompletedAt == "" {
		t.Fatalf("unexpected failure result: %#v, %v", result, err)
	}
}
