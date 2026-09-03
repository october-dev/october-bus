package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCrossRepositoryExample(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"workspaces: frontend=",
		"discovery: frontend found backend; backend found frontend",
		"bounded context: GET /api/profile adds displayName: string",
		"delegation: request ",
		"dependencies: frontend task ",
		"reply: The profile response now includes displayName.",
		"receipts: request=acknowledged response=acknowledged",
		"task state: Add displayName to the profile API=done",
		"task state: Render displayName in the profile UI=done",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}
