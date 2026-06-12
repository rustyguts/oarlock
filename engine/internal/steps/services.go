package steps

import (
	"context"

	"github.com/google/uuid"
)

// Services are the workspace-scoped resolvers executors need. Secrets are
// referenced by name and resolved at execution time — never inlined in
// definitions (hard rule 6).
type Services struct {
	Secrets SecretSource
	MCP     MCPSource
}

type SecretSource interface {
	// APIKey resolves an api_key-typed secret (BYOK for ai.* steps).
	APIKey(ctx context.Context, workspaceID uuid.UUID, name string) (provider, key string, err error)
	// WorkspaceSecrets decrypts all secrets for expression context + redaction.
	WorkspaceSecrets(ctx context.Context, workspaceID uuid.UUID) (map[string]string, error)
}

type MCPSource interface {
	Server(ctx context.Context, workspaceID uuid.UUID, name string) (url, authHeader string, err error)
}
