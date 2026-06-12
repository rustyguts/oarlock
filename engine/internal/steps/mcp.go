package steps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rustyguts/oarlock/engine/internal/mcpclient"
)

// MCPTool calls one tool on a workspace-registered MCP server.
type MCPTool struct {
	svc *Services
}

func (e *MCPTool) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	serverName, _ := in.Config["server"].(string)
	toolName, _ := in.Config["tool"].(string)
	if serverName == "" || toolName == "" {
		return TaskOutput{}, fmt.Errorf("mcp.tool: server and tool are required")
	}

	args := map[string]any{}
	switch v := in.Config["arguments"].(type) {
	case string:
		if v != "" {
			if err := json.Unmarshal([]byte(v), &args); err != nil {
				return TaskOutput{}, fmt.Errorf("mcp.tool: arguments must be a JSON object: %w", err)
			}
		}
	case map[string]any:
		args = v
	case nil:
	default:
		return TaskOutput{}, fmt.Errorf("mcp.tool: arguments must be a JSON object")
	}

	url, auth, err := e.svc.MCP.Server(ctx, in.WorkspaceID, serverName)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("mcp.tool: %w", err)
	}

	in.Log.Info("mcp tool call", "server", serverName, "tool", toolName)
	result, err := mcpclient.CallTool(ctx, url, auth, toolName, args)
	if err != nil {
		return TaskOutput{}, err
	}
	in.Log.Info("mcp tool done", "server", serverName, "tool", toolName)
	return TaskOutput{Data: result}, nil
}
