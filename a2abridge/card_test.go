package a2abridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/october-dev/october-bus/a2abridge"
	"github.com/october-dev/october-bus/bus"
)

func TestAgentCardMapsPublicAgentFields(t *testing.T) {
	card, err := a2abridge.NewAgentCard(bus.Agent{
		ID:          "reviewer",
		DisplayName: "Reviewer",
		Capabilities: []bus.AgentCapability{
			{Name: "code_review", Description: "Reviews code changes."},
		},
		ExecutionID: "execution-secret",
	}, a2abridge.CardOptions{
		InterfaceURL: "https://agents.example.com/reviewer",
		Version:      "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "Reviewer" || card.Version != "1.2.3" || len(card.Skills) != 1 {
		t.Fatalf("unexpected card: %#v", card)
	}
	if card.Skills[0].ID != "code_review" || card.Skills[0].Description != "Reviews code changes." {
		t.Fatalf("unexpected skills: %#v", card.Skills)
	}
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].ProtocolBinding != a2a.TransportProtocolHTTPJSON || card.SupportedInterfaces[0].ProtocolVersion != a2a.Version {
		t.Fatalf("unexpected interfaces: %#v", card.SupportedInterfaces)
	}
	if _, ok := card.SecuritySchemes["bearer"].(a2a.HTTPAuthSecurityScheme); !ok {
		t.Fatalf("bearer security scheme is missing: %#v", card.SecuritySchemes)
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"execution-secret", "scopeToken", "agentToken"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("card contains private value %q", secret)
		}
	}
}

func TestAgentCardRequiresSecureRemoteURL(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	for _, value := range []string{
		"http://example.com/a2a",
		"https://user:secret@example.com/a2a",
		"https://example.com/a2a?token=secret",
		"file:///tmp/a2a",
	} {
		if _, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{InterfaceURL: value}); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	for _, value := range []string{
		"http://127.0.0.1:8080/a2a",
		"http://[::1]:8080/a2a",
		"http://localhost:8080/a2a",
		"https://agents.example.com/a2a",
	} {
		if _, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{InterfaceURL: value}); err != nil {
			t.Fatalf("expected %q to be accepted: %v", value, err)
		}
	}
}

func TestAgentCardProviderFields(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	card, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{
		InterfaceURL:         "https://agents.example.com/reviewer",
		ProviderOrganization: "Example Labs",
		ProviderURL:          "https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.Provider == nil {
		t.Fatal("Provider is nil, want populated")
	}
	if card.Provider.Org != "Example Labs" {
		t.Errorf("Provider.Org = %q, want %q", card.Provider.Org, "Example Labs")
	}
	if card.Provider.URL != "https://example.com" {
		t.Errorf("Provider.URL = %q, want %q", card.Provider.URL, "https://example.com")
	}
}

func TestAgentCardProviderBothFieldsRequired(t *testing.T) {
	// Per A2A spec §AgentProvider, organization and url are both required
	// when the provider block is present. Setting only one must be rejected.
	agent := bus.Agent{DisplayName: "Reviewer"}
	for _, tc := range []struct {
		name    string
		options a2abridge.CardOptions
		wantErr string
	}{
		{
			name: "organization-only",
			options: a2abridge.CardOptions{
				InterfaceURL:         "https://agents.example.com/reviewer",
				ProviderOrganization: "Example Labs",
			},
			wantErr: "providerOrganization and providerUrl must both be set",
		},
		{
			name: "url-only",
			options: a2abridge.CardOptions{
				InterfaceURL: "https://agents.example.com/reviewer",
				ProviderURL:  "https://example.com",
			},
			wantErr: "providerOrganization and providerUrl must both be set",
		},
		{
			name: "whitespace-only-organization",
			options: a2abridge.CardOptions{
				InterfaceURL:         "https://agents.example.com/reviewer",
				ProviderOrganization: "   ",
				ProviderURL:          "https://example.com",
			},
			wantErr: "providerOrganization cannot be whitespace-only",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card, err := a2abridge.NewAgentCard(agent, tc.options)
			if err == nil {
				t.Fatalf("expected error, got card: %#v", card)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestAgentCardProviderOmittedWhenUnset(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	card, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{
		InterfaceURL: "https://agents.example.com/reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.Provider != nil {
		t.Errorf("Provider = %+v, want nil when neither ProviderOrganization nor ProviderURL is set", card.Provider)
	}
}

func TestAgentCardDocumentationAndIconURLs(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	card, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{
		InterfaceURL:     "https://agents.example.com/reviewer",
		DocumentationURL: "https://docs.example.com/reviewer",
		IconURL:          "https://cdn.example.com/reviewer.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.DocumentationURL != "https://docs.example.com/reviewer" {
		t.Errorf("DocumentationURL = %q, want %q", card.DocumentationURL, "https://docs.example.com/reviewer")
	}
	if card.IconURL != "https://cdn.example.com/reviewer.png" {
		t.Errorf("IconURL = %q, want %q", card.IconURL, "https://cdn.example.com/reviewer.png")
	}
}

func TestAgentCardDocumentationAndIconOmittedWhenUnset(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	card, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{
		InterfaceURL: "https://agents.example.com/reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.DocumentationURL != "" {
		t.Errorf("DocumentationURL = %q, want empty", card.DocumentationURL)
	}
	if card.IconURL != "" {
		t.Errorf("IconURL = %q, want empty", card.IconURL)
	}
	// Confirm the omitted fields stay absent from the public JSON.
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"\"documentationUrl\"", "\"iconUrl\"", "\"provider\""} {
		if strings.Contains(string(encoded), unwanted) {
			t.Errorf("encoded card contains %q but should omit it: %s", unwanted, encoded)
		}
	}
}

// TestAgentCardRejectsInvalidPublicURL exercises validatePublicURL through
// every field that uses it. Each case must return a clear, field-prefixed
// error and must not produce a card.
func TestAgentCardRejectsInvalidPublicURL(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	cases := []struct {
		name    string
		options a2abridge.CardOptions
		wantErr string
	}{
		{
			name: "provider-url-credentials",
			options: a2abridge.CardOptions{
				InterfaceURL: "https://agents.example.com/reviewer",
				ProviderURL:  "https://user:placeholder@example.com",
			},
			wantErr: "providerUrl",
		},
		{
			name: "documentation-url-file-scheme",
			options: a2abridge.CardOptions{
				InterfaceURL:     "https://agents.example.com/reviewer",
				DocumentationURL: "file:///etc/passwd",
			},
			wantErr: "documentationUrl",
		},
		{
			name: "icon-url-query-string",
			options: a2abridge.CardOptions{
				InterfaceURL: "https://agents.example.com/reviewer",
				IconURL:      "https://cdn.example.com/icon.png?token=placeholder",
			},
			wantErr: "iconUrl",
		},
		{
			name: "documentation-url-fragment",
			options: a2abridge.CardOptions{
				InterfaceURL:     "https://agents.example.com/reviewer",
				DocumentationURL: "https://docs.example.com/reviewer#placeholder",
			},
			wantErr: "documentationUrl",
		},
		{
			name: "provider-url-relative",
			options: a2abridge.CardOptions{
				InterfaceURL: "https://agents.example.com/reviewer",
				ProviderURL:  "/relative/path",
			},
			wantErr: "providerUrl",
		},
		{
			name: "icon-url-empty-host",
			options: a2abridge.CardOptions{
				InterfaceURL: "https://agents.example.com/reviewer",
				IconURL:      "https://",
			},
			wantErr: "iconUrl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card, err := a2abridge.NewAgentCard(agent, tc.options)
			if err == nil {
				t.Fatalf("expected error, got card: %#v", card)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain field name %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestAgentCardPublicURLsAllowPlainHTTP checks the carve-out from
// validateInterfaceURL: a documentation/icon/provider URL on plain HTTP is
// allowed because it's a link clients follow rather than an endpoint they
// POST to. The interface URL still requires HTTPS for non-loopback hosts.
func TestAgentCardPublicURLsAllowPlainHTTP(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	card, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{
		InterfaceURL:         "http://127.0.0.1:8080/a2a",
		ProviderOrganization: "Example Labs",
		ProviderURL:          "http://example.com",
		DocumentationURL:    "http://docs.internal/reviewer",
		IconURL:             "http://cdn.internal/icon.png",
	})
	if err != nil {
		t.Fatalf("plain-http public URLs should be accepted, got %v", err)
	}
	if card.DocumentationURL != "http://docs.internal/reviewer" {
		t.Errorf("DocumentationURL = %q", card.DocumentationURL)
	}
	if card.IconURL != "http://cdn.internal/icon.png" {
		t.Errorf("IconURL = %q", card.IconURL)
	}
	if card.Provider == nil || card.Provider.URL != "http://example.com" || card.Provider.Org != "Example Labs" {
		t.Errorf("Provider not propagated correctly: %+v", card.Provider)
	}
}

func TestAgentCardFullConfigurationRoundTrip(t *testing.T) {
	agent := bus.Agent{DisplayName: "Reviewer"}
	card, err := a2abridge.NewAgentCard(agent, a2abridge.CardOptions{
		InterfaceURL:         "https://agents.example.com/reviewer",
		ProviderOrganization: "Example Labs",
		ProviderURL:          "https://example.com",
		DocumentationURL:     "https://docs.example.com/reviewer",
		IconURL:              "https://cdn.example.com/reviewer.png",
		Version:              "1.2.3",
		Description:          "Reviews code changes.",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, want := range []string{
		`"organization":"Example Labs"`,
		`"url":"https://example.com"`,
		`"documentationUrl":"https://docs.example.com/reviewer"`,
		`"iconUrl":"https://cdn.example.com/reviewer.png"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("encoded card missing %q\n--- card ---\n%s", want, body)
		}
	}
	// Defence in depth — credentials and execution IDs must never appear in
	// the public card even when every optional field is configured.
	for _, secret := range []string{"execution-secret", "scopeToken", "agentToken"} {
		if strings.Contains(body, secret) {
			t.Fatalf("encoded card contains private value %q", secret)
		}
	}
}

func TestAgentCardHandlerWorksWithOfficialResolver(t *testing.T) {
	card, err := a2abridge.NewAgentCard(bus.Agent{DisplayName: "Reviewer"}, a2abridge.CardOptions{
		InterfaceURL: "https://agents.example.com/reviewer",
		Version:      "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := a2abridge.NewAgentCardHandler(card)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(a2abridge.AgentCardPath, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	resolver := agentcard.NewResolver(server.Client())
	resolved, err := resolver.Resolve(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name != card.Name || resolved.Version != card.Version || len(resolved.SupportedInterfaces) != 1 {
		t.Fatalf("unexpected resolved card: %#v", resolved)
	}

	response, err := server.Client().Get(server.URL + a2abridge.AgentCardPath)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.Header.Get("Cache-Control") != "public, max-age=60" || response.Header.Get("ETag") == "" || response.Header.Get("Last-Modified") == "" {
		t.Fatalf("missing cache headers: %#v", response.Header)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+a2abridge.AgentCardPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("If-None-Match", response.Header.Get("ETag"))
	cached, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	cached.Body.Close()
	if cached.StatusCode != http.StatusNotModified {
		t.Fatalf("unexpected cache response: %d", cached.StatusCode)
	}
}

func TestAgentCardHandlerRejectsUnsupportedMethods(t *testing.T) {
	card, err := a2abridge.NewAgentCard(bus.Agent{DisplayName: "Reviewer"}, a2abridge.CardOptions{
		InterfaceURL: "https://agents.example.com/reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := a2abridge.NewAgentCardHandler(card)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, a2abridge.AgentCardPath, nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD, OPTIONS" {
		t.Fatalf("unexpected response: %d, %q", response.Code, response.Header().Get("Allow"))
	}
}
