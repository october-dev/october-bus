package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/october-dev/october-bus/bus"
)

const (
	mcpBridgeInstructions = "This server forwards October Bus tools from the current agent execution."
	mcpBridgeNoIdentity   = "October Bus tools are unavailable because this process is not running inside a managed agent execution."
)

type agentTokenTransport struct {
	token string
	base  http.RoundTripper
}

func (transport agentTokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func runMCPStdio(ctx context.Context) error {
	address := strings.TrimRight(strings.TrimSpace(os.Getenv("OCTOBER_BUS_ADDRESS")), "/")
	token := strings.TrimSpace(os.Getenv("OCTOBER_BUS_AGENT_TOKEN"))
	if address == "" || token == "" {
		server := newMCPBridgeServer(mcpBridgeNoIdentity)
		return server.Run(ctx, &mcp.StdioTransport{})
	}

	connectContext, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	httpClient := &http.Client{Transport: agentTokenTransport{token: token, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "october-bus-stdio-bridge", Version: bus.Version}, nil)
	upstream, err := client.Connect(connectContext, &mcp.StreamableClientTransport{
		Endpoint:             address + "/mcp",
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		cancelConnect()
		return fmt.Errorf("could not connect to October Bus at %s: %w", address, err)
	}
	defer upstream.Close()

	server := newMCPBridgeServer(mcpBridgeInstructions)
	for cursor := ""; ; {
		result, err := upstream.ListTools(connectContext, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			cancelConnect()
			return fmt.Errorf("could not discover October Bus tools: %w", err)
		}
		for _, upstreamTool := range result.Tools {
			tool := *upstreamTool
			toolName := tool.Name
			server.AddTool(&tool, func(callContext context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				params := request.Params
				return upstream.CallTool(callContext, &mcp.CallToolParams{
					Meta:           params.Meta,
					Name:           toolName,
					Arguments:      params.Arguments,
					InputResponses: params.InputResponses,
					RequestState:   params.RequestState,
				})
			})
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor {
			cancelConnect()
			return errors.New("October Bus returned a repeated tool cursor")
		}
		cursor = result.NextCursor
	}
	cancelConnect()
	return server.Run(ctx, &mcp.StdioTransport{})
}

func newMCPBridgeServer(instructions string) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "october-bus", Version: bus.Version}, &mcp.ServerOptions{
		Instructions: instructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
}
