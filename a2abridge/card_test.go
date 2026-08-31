package a2abridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// makeTestCard returns a small valid card used by the cache-policy tests
// below. Each test calls it fresh so failures cannot bleed between cases.
func makeTestCard(t *testing.T) *a2a.AgentCard {
	t.Helper()
	card, err := a2abridge.NewAgentCard(bus.Agent{DisplayName: "Reviewer"}, a2abridge.CardOptions{
		InterfaceURL: "https://agents.example.com/reviewer",
		Version:      "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func TestAgentCardHandlerHonoursCustomCacheLifetime(t *testing.T) {
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil))
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want %q", got, "public, max-age=300")
	}
}

func TestAgentCardHandlerSupportsZeroCacheLifetime(t *testing.T) {
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil))
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=0" {
		t.Errorf("Cache-Control = %q, want %q", got, "public, max-age=0")
	}
}

func TestAgentCardHandlerRejectsNegativeCacheLifetime(t *testing.T) {
	_, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: -time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("expected negative-lifetime rejection, got %v", err)
	}
}

func TestAgentCardHandlerRejectsExcessiveCacheLifetime(t *testing.T) {
	_, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: 48 * time.Hour,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
		t.Fatalf("expected excessive-lifetime rejection, got %v", err)
	}
}

func TestAgentCardHandlerHonoursFixedLastModified(t *testing.T) {
	fixed := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: time.Minute,
		LastModified:  fixed,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil))
	wantLastModified := fixed.Format(http.TimeFormat)
	if got := response.Header().Get("Last-Modified"); got != wantLastModified {
		t.Errorf("Last-Modified = %q, want %q", got, wantLastModified)
	}

	// Conditional GET against a time matching the card should return 304.
	conditional := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil)
	request.Header.Set("If-Modified-Since", wantLastModified)
	handler.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified {
		t.Errorf("If-Modified-Since match: status = %d, want %d", conditional.Code, http.StatusNotModified)
	}
}

func TestAgentCardHandlerIfModifiedSinceMissReturnsFullResponse(t *testing.T) {
	fixed := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: time.Minute,
		LastModified:  fixed,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The client claims to have a version older than the card's last update.
	stale := fixed.Add(-time.Hour).Format(http.TimeFormat)
	request := httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil)
	request.Header.Set("If-Modified-Since", stale)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Errorf("stale If-Modified-Since: status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.Len() == 0 {
		t.Error("stale If-Modified-Since should return a body, got empty")
	}
}

func TestAgentCardHandlerIfModifiedSinceInvalidDateTreatedAsMiss(t *testing.T) {
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil)
	request.Header.Set("If-Modified-Since", "not-a-valid-http-date")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Errorf("invalid If-Modified-Since: status = %d, want %d (full response)", response.Code, http.StatusOK)
	}
}

func TestAgentCardHandlerIfNoneMatchPrecedesIfModifiedSince(t *testing.T) {
	fixed := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: time.Minute,
		LastModified:  fixed,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil)
	request.Header.Set("If-None-Match", `"different"`)
	request.Header.Set("If-Modified-Since", fixed.Format(http.TimeFormat))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("non-matching If-None-Match must ignore matching date: status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestAgentCardHandlerSupportsStandardIfNoneMatchForms(t *testing.T) {
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	initial := httptest.NewRecorder()
	handler.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil))
	etag := initial.Header().Get("ETag")
	for name, value := range map[string]string{
		"weak":     "W/" + etag,
		"list":     `"different", ` + etag,
		"wildcard": "*",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil)
			request.Header.Set("If-None-Match", value)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotModified {
				t.Fatalf("If-None-Match %q: status = %d, want %d", value, response.Code, http.StatusNotModified)
			}
		})
	}
}

func TestAgentCardHandlerCacheHeadersStayConsistent(t *testing.T) {
	fixed := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	handler, err := a2abridge.NewAgentCardHandlerWithOptions(makeTestCard(t), a2abridge.HandlerOptions{
		CacheLifetime: 30 * time.Second,
		LastModified:  fixed,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Issue several requests and confirm the trio (ETag, Cache-Control,
	// Last-Modified) is byte-identical across them.
	var firstETag, firstCacheControl, firstLastModified string
	for i := 0; i < 5; i++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, a2abridge.AgentCardPath, nil))
		if firstETag == "" {
			firstETag = response.Header().Get("ETag")
			firstCacheControl = response.Header().Get("Cache-Control")
			firstLastModified = response.Header().Get("Last-Modified")
			continue
		}
		if got := response.Header().Get("ETag"); got != firstETag {
			t.Errorf("request %d: ETag drifted to %q (was %q)", i, got, firstETag)
		}
		if got := response.Header().Get("Cache-Control"); got != firstCacheControl {
			t.Errorf("request %d: Cache-Control drifted to %q (was %q)", i, got, firstCacheControl)
		}
		if got := response.Header().Get("Last-Modified"); got != firstLastModified {
			t.Errorf("request %d: Last-Modified drifted to %q (was %q)", i, got, firstLastModified)
		}
	}
}
