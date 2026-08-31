package a2abridge

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/october-dev/october-bus/bus"
)

const bearerSchemeName a2a.SecuritySchemeName = "bearer"

// CardOptions configures the content of a generated A2A Agent Card. The HTTP
// delivery behaviour of the resulting card is controlled separately by
// HandlerOptions in a2abridge/handler.go.
type CardOptions struct {
	InterfaceURL string

	// ProviderOrganization names the organization that publishes the agent.
	// Both ProviderOrganization and ProviderURL must be set together to include
	// a Provider block in the generated card. A whitespace-only value is
	// rejected; set neither field to omit the Provider block entirely.
	ProviderOrganization string

	// ProviderURL points at the provider's public website or relevant
	// documentation. Both ProviderOrganization and ProviderURL must be set
	// together; setting only one is rejected.
	ProviderURL string

	// DocumentationURL is an optional link to the agent's documentation.
	DocumentationURL string

	// IconURL is an optional URL to an icon representing the agent.
	IconURL string

	Version     string
	Description string
}

func NewAgentCard(agent bus.Agent, options CardOptions) (*a2a.AgentCard, error) {
	if strings.TrimSpace(agent.DisplayName) == "" {
		return nil, errors.New("agent display name is required")
	}
	if err := validateInterfaceURL(options.InterfaceURL); err != nil {
		return nil, err
	}
	// Require both provider fields together, per A2A spec §AgentProvider:
	// organization and url are both required when the provider block is present.
	// Whitespace-only organization is also rejected.
	if options.ProviderOrganization != "" && strings.TrimSpace(options.ProviderOrganization) == "" {
		return nil, errors.New("providerOrganization cannot be whitespace-only")
	}
	if options.ProviderOrganization == "" && options.ProviderURL != "" {
		return nil, errors.New("providerOrganization and providerUrl must both be set or both omitted")
	}
	if options.ProviderOrganization != "" && options.ProviderURL == "" {
		return nil, errors.New("providerOrganization and providerUrl must both be set or both omitted")
	}
	for _, field := range []struct {
		name, value string
	}{
		{"providerUrl", options.ProviderURL},
		{"documentationUrl", options.DocumentationURL},
		{"iconUrl", options.IconURL},
	} {
		if field.value != "" {
			if err := validatePublicURL(field.value); err != nil {
				return nil, errors.New(field.name + ": " + err.Error())
			}
		}
	}
	if options.Version == "" {
		options.Version = bus.Version
	}
	if options.Description == "" {
		options.Description = "AI agent connected through October Bus."
	}

	skills := make([]a2a.AgentSkill, 0, len(agent.Capabilities))
	for _, capability := range agent.Capabilities {
		description := capability.Description
		if description == "" {
			description = "Supports " + capability.Name + "."
		}
		skills = append(skills, a2a.AgentSkill{
			ID:          capability.Name,
			Name:        capability.Name,
			Description: description,
			Tags:        []string{capability.Name},
		})
	}

	card := &a2a.AgentCard{
		Name:        agent.DisplayName,
		Description: options.Description,
		Version:     options.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(options.InterfaceURL, a2a.TransportProtocolHTTPJSON),
		},
		Capabilities:       a2a.AgentCapabilities{},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             skills,
		SecuritySchemes: a2a.NamedSecuritySchemes{
			bearerSchemeName: a2a.HTTPAuthSecurityScheme{
				Scheme:      "Bearer",
				Description: "October Bus agent credential.",
			},
		},
		SecurityRequirements: a2a.SecurityRequirementsOptions{
			{bearerSchemeName: a2a.SecuritySchemeScopes{}},
		},
	}
	if options.ProviderOrganization != "" || options.ProviderURL != "" {
		card.Provider = &a2a.AgentProvider{
			Org: options.ProviderOrganization,
			URL: options.ProviderURL,
		}
	}
	card.DocumentationURL = options.DocumentationURL
	card.IconURL = options.IconURL
	return card, nil
}

func validateInterfaceURL(value string) error {
	if len(value) > 4096 {
		return errors.New("A2A interface URL exceeds 4096 bytes")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("A2A interface URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("A2A interface URL must not include credentials, a query, or a fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return errors.New("non-loopback A2A interfaces require HTTPS")
	}
	return nil
}

// validatePublicURL checks that a URL embedded in a public Agent Card is
// safe to publish. It is stricter than a generic URL parser because cards
// are read by other agents, cached by browsers, and indexed by search
// engines. The interface URL uses a tighter contract (validateInterfaceURL)
// because clients actually call it.
func validatePublicURL(value string) error {
	if len(value) > 4096 {
		return errors.New("URL exceeds 4096 bytes")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must not include credentials, a query, or a fragment")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
