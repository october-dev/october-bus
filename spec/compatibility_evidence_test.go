package spec_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

// Validate history and unregistered records too: excluding a malformed record
// from registry.json must not hide it from CI. Attempt notes belong elsewhere.
func validateEvidenceDirectory(directory string, schema *jsonschema.Resolved) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	namePattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.json$`)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !namePattern.MatchString(entry.Name()) {
			return fmt.Errorf("%s: evidence must be flat, regular JSON files", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return err
		}
		var record any
		if !utf8.Valid(data) || json.Unmarshal(data, &record) != nil {
			return fmt.Errorf("%s: invalid evidence JSON", entry.Name())
		}
		if err := schema.Validate(record); err != nil {
			return fmt.Errorf("%s: invalid compatibility evidence: %w", entry.Name(), err)
		}
	}
	return nil
}

func TestAllCompatibilityEvidence(t *testing.T) {
	schema := resolvedSchema(t, filepath.Join("0.1", "schemas", "compatibility-evidence.schema.json"), "")
	if err := validateEvidenceDirectory(filepath.Join("..", "compatibility", "evidence"), schema); err != nil {
		t.Fatal(err)
	}
}

func TestUnregisteredCompatibilityEvidenceIsValidated(t *testing.T) {
	schema := resolvedSchema(t, filepath.Join("0.1", "schemas", "compatibility-evidence.schema.json"), "")
	fixture := readJSON(t, filepath.Join("..", "compatibility", "evidence", "codex-0.152.1-macos-arm64.json")).(map[string]any)
	for _, outcome := range []string{"passed", "failed", "observed", "partial", "not-run"} {
		t.Run(outcome, func(t *testing.T) {
			directory := t.TempDir() // Deliberately no registry entry for this record.
			fixture["result"] = outcome
			data, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "unregistered.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			err = validateEvidenceDirectory(directory, schema)
			if (err == nil) != (outcome == "passed" || outcome == "failed") {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
	for _, name := range []string{"bundle.json", "notes.md", "malformed.json"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			data := []byte(`{"outcome":"not-run"}`)
			if name == "malformed.json" {
				data = []byte(`{"result":`)
			}
			if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateEvidenceDirectory(directory, schema); err == nil {
				t.Fatal("attempt metadata or notes were accepted as formal evidence")
			}
		})
	}
	t.Run("nested-record", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Mkdir(filepath.Join(directory, "nested"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := validateEvidenceDirectory(directory, schema); err == nil {
			t.Fatal("nested evidence directory was accepted")
		}
	})
}
