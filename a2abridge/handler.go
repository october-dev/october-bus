package a2abridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const (
	AgentCardPath = "/.well-known/agent-card.json"

	// DefaultAgentCardCacheLifetime is the Cache-Control max-age applied by
	// NewAgentCardHandler when the caller does not specify a value. It
	// matches the conservative lifetime the A2A bridge shipped with before
	// the policy became configurable.
	DefaultAgentCardCacheLifetime = 60 * time.Second

	// MaxAgentCardCacheLifetime caps the cache lifetime a caller can
	// request. Twenty-four hours is generous for any realistic agent card
	// and prevents a misconfigured caller from effectively pinning a card
	// forever.
	MaxAgentCardCacheLifetime = 24 * time.Hour
)

// HandlerOptions controls HTTP delivery behaviour for the Agent Card handler.
// Card *content* lives on CardOptions; these options only shape caching and
// conditional request handling.
type HandlerOptions struct {
	// CacheLifetime is the max-age value emitted in the Cache-Control
	// response header. Zero means "revalidate every time." Negative values
	// and values greater than MaxAgentCardCacheLifetime are rejected at
	// construction time.
	CacheLifetime time.Duration

	// LastModified overrides the Last-Modified response header. When zero,
	// the handler uses the current time truncated to one second, matching
	// the original bridge behaviour. Setting a fixed time is useful for
	// cards that are pinned to a known release.
	LastModified time.Time
}

// NewAgentCardHandler returns an http.Handler that serves the given Agent
// Card with the default cache policy (60-second max-age, the current time
// as Last-Modified). Use NewAgentCardHandlerWithOptions to override either
// value.
func NewAgentCardHandler(card *a2a.AgentCard) (http.Handler, error) {
	return NewAgentCardHandlerWithOptions(card, HandlerOptions{CacheLifetime: DefaultAgentCardCacheLifetime})
}

// NewAgentCardHandlerWithOptions returns an http.Handler that serves the
// given Agent Card with a configurable cache policy. The ETag, Cache-Control
// and Last-Modified headers are computed once at construction time and stay
// consistent for every request handled by the returned http.Handler.
//
// The handler honours both If-None-Match (compared against the ETag) and
// If-Modified-Since (compared against Last-Modified), returning 304 Not
// Modified when either matches.
func NewAgentCardHandlerWithOptions(card *a2a.AgentCard, options HandlerOptions) (http.Handler, error) {
	if card == nil {
		return nil, errors.New("agent card is required")
	}
	if options.CacheLifetime < 0 {
		return nil, fmt.Errorf("agent card cache lifetime must not be negative, got %s", options.CacheLifetime)
	}
	if options.CacheLifetime > MaxAgentCardCacheLifetime {
		return nil, fmt.Errorf("agent card cache lifetime %s exceeds the maximum of %s", options.CacheLifetime, MaxAgentCardCacheLifetime)
	}
	body, err := json.Marshal(card)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	cacheControl := fmt.Sprintf("public, max-age=%d", int(options.CacheLifetime.Seconds()))
	lastModified := options.LastModified
	if lastModified.IsZero() {
		lastModified = time.Now().UTC()
	}
	lastModifiedHeader := lastModified.UTC().Truncate(time.Second).Format(http.TimeFormat)

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Allow", "GET, HEAD, OPTIONS")
		response.Header().Set("Access-Control-Allow-Origin", "*")
		response.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		response.Header().Set("Cache-Control", cacheControl)
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("ETag", etag)
		response.Header().Set("Last-Modified", lastModifiedHeader)

		switch request.Method {
		case http.MethodOptions:
			response.WriteHeader(http.StatusNoContent)
		case http.MethodGet, http.MethodHead:
			if requestMatchesCache(request, etag, lastModified) {
				response.WriteHeader(http.StatusNotModified)
				return
			}
			response.WriteHeader(http.StatusOK)
			if request.Method == http.MethodGet {
				_, _ = response.Write(body)
			}
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}), nil
}

// requestMatchesCache evaluates If-None-Match before If-Modified-Since, as
// required by HTTP. If-Modified-Since is ignored whenever If-None-Match is
// present, including when none of its entity tags match.
func requestMatchesCache(request *http.Request, etag string, lastModified time.Time) bool {
	if values := request.Header.Values("If-None-Match"); len(values) > 0 {
		return ifNoneMatchMatches(values, etag)
	}
	header := request.Header.Get("If-Modified-Since")
	if header == "" {
		return false
	}
	since, err := http.ParseTime(header)
	if err != nil {
		// Per RFC 7232 an invalid date is treated as "never matches" so the
		// caller receives the full response rather than a misleading 304.
		return false
	}
	// Compare at second precision: Last-Modified is truncated to one second
	// and If-Modified-Since has only second precision in HTTP. Per RFC 7232
	// §3.3 the condition matches when the resource's last modification date
	// is earlier than or equal to the client-supplied date — that is, when
	// the client's copy is at least as new as the resource.
	return !since.Truncate(time.Second).Before(lastModified.Truncate(time.Second))
}

// ifNoneMatchMatches uses weak entity-tag comparison for GET and HEAD cache
// validation. It accepts multiple header lines, comma-separated lists, weak
// tags, and the wildcard form.
func ifNoneMatchMatches(values []string, etag string) bool {
	target := strings.TrimPrefix(strings.TrimSpace(etag), "W/")
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" {
				return true
			}
			if strings.TrimPrefix(candidate, "W/") == target {
				return true
			}
		}
	}
	return false
}
