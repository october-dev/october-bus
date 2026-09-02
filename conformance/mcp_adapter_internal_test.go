package conformance

import "testing"

func TestRemoveEnvironmentValues(t *testing.T) {
	environment := []string{
		"PATH=/usr/bin",
		"CUSTOM_ADMIN=admin-secret",
		"CUSTOM_SCOPE=scope-secret",
		"OTHER=value",
	}
	filtered := removeEnvironmentValues(environment, "admin-secret", "scope-secret")
	if len(filtered) != 2 || filtered[0] != "PATH=/usr/bin" || filtered[1] != "OTHER=value" {
		t.Fatalf("unexpected filtered environment: %#v", filtered)
	}
}
