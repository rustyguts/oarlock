// Package mcpclient is a thin wrapper over the official MCP Go SDK for
// calling external MCP servers over streamable HTTP.
package mcpclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type authTransport struct {
	header string
	base   http.RoundTripper
}

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.header != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", t.header)
	}
	return t.base.RoundTrip(r)
}

func connect(ctx context.Context, url, authHeader string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "oarlock", Version: "0.1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: url,
		HTTPClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: authTransport{header: authHeader, base: http.DefaultTransport},
		},
		DisableStandaloneSSE: true, // request/response only; no server push needed
		MaxRetries:           -1,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %s: %w", url, err)
	}
	return session, nil
}

// ListTools connects, lists the server's tools, and disconnects.
func ListTools(ctx context.Context, url, authHeader string) ([]ToolInfo, error) {
	session, err := connect(ctx, url, authHeader)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}
	out := make([]ToolInfo, 0, len(res.Tools))
	for _, t := range res.Tools {
		out = append(out, ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out, nil
}

// CallTool invokes one tool and returns a JSON-friendly result: the
// structured content when the server provides it, otherwise the text content
// (joined if multiple).
func CallTool(ctx context.Context, url, authHeader, tool string, args map[string]any) (any, error) {
	session, err := connect(ctx, url, authHeader)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("mcp tools/call %s: %w", tool, err)
	}

	var texts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, t.Text)
		}
	}
	text := strings.Join(texts, "\n")

	if res.IsError {
		if text == "" {
			text = "tool returned an error"
		}
		return nil, fmt.Errorf("mcp tool %s: %s", tool, text)
	}
	if res.StructuredContent != nil {
		return res.StructuredContent, nil
	}
	return map[string]any{"text": text}, nil
}
