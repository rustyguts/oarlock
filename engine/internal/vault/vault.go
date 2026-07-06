// Package vault encrypts workspace secrets (design hard rule 6: secrets only
// via the vault, never in definitions or logs) and resolves them for step
// executors. v0 is AES-256-GCM under a single master key from the
// environment; per-row data keys (true envelope encryption) can swap in later
// behind the same key_id mechanism.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const KeyID = "local-v1"

// devKey keeps `make up` working with zero setup; production deployments set
// OARLOCK_MASTER_KEY (64 hex chars).
const devKey = "0b5e8836f9aeaf6e6a3e2bd1b1f0c39d8a1c2e4f5a6b7c8d9e0f1a2b3c4d5e6f"

type Vault struct {
	Pool        *pgxpool.Pool
	key         []byte
	usingDevKey bool
}

func New(pool *pgxpool.Pool, hexKey string, log *slog.Logger) (*Vault, error) {
	usingDevKey := false
	if hexKey == "" {
		hexKey = devKey
		usingDevKey = true
		log.Warn("OARLOCK_MASTER_KEY not set; using built-in dev key — do not store real secrets")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("OARLOCK_MASTER_KEY must be 64 hex chars (32 bytes)")
	}
	return &Vault{Pool: pool, key: key, usingDevKey: usingDevKey}, nil
}

// DevKey reports whether the built-in dev master key is in use (no
// OARLOCK_MASTER_KEY set). The UI surfaces a warning banner so real secrets
// aren't stored under a key that ships in the source tree.
func (v *Vault) DevKey() bool { return v.usingDevKey }

func (v *Vault) Encrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (v *Vault) Decrypt(sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}

type secretPayload struct {
	Value  string `json:"value"`
	APIKey string `json:"api_key,omitempty"` // legacy payload shape (pre-secrets)
}

func (p secretPayload) value() string {
	if p.Value != "" {
		return p.Value
	}
	return p.APIKey
}

func (v *Vault) SealSecret(value string) ([]byte, error) {
	raw, _ := json.Marshal(secretPayload{Value: value})
	return v.Encrypt(raw)
}

func (v *Vault) openSecret(name string, sealed []byte) (string, error) {
	raw, err := v.Decrypt(sealed)
	if err != nil {
		return "", fmt.Errorf("secret %q: decrypt failed (master key changed?)", name)
	}
	var p secretPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	return p.value(), nil
}

// APIKey resolves an api_key-typed secret for ai.* executors.
func (v *Vault) APIKey(ctx context.Context, workspaceID uuid.UUID, name string) (provider, key string, err error) {
	var sealed []byte
	err = v.Pool.QueryRow(ctx, `
		select provider, encrypted_data from secrets
		where workspace_id = $1 and name = $2 and type = 'api_key'`, workspaceID, name).Scan(&provider, &sealed)
	if err != nil {
		return "", "", fmt.Errorf("API key secret %q not found", name)
	}
	key, err = v.openSecret(name, sealed)
	return provider, key, err
}

// WorkspaceSecrets decrypts every secret in the workspace — the engine binds
// them as the `secrets` context for expressions and uses the values to redact
// task records and logs.
func (v *Vault) WorkspaceSecrets(ctx context.Context, workspaceID uuid.UUID) (map[string]string, error) {
	rows, err := v.Pool.Query(ctx, `
		select name, encrypted_data from secrets where workspace_id = $1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name string
		var sealed []byte
		if err := rows.Scan(&name, &sealed); err != nil {
			return nil, err
		}
		value, err := v.openSecret(name, sealed)
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, rows.Err()
}

// Server resolves a named, enabled MCP server to its endpoint + auth header.
func (v *Vault) Server(ctx context.Context, workspaceID uuid.UUID, name string) (url, authHeader string, err error) {
	var sealed []byte
	var enabled bool
	err = v.Pool.QueryRow(ctx, `
		select url, auth_encrypted, is_enabled from mcp_servers
		where workspace_id = $1 and name = $2`, workspaceID, name).Scan(&url, &sealed, &enabled)
	if err != nil {
		return "", "", fmt.Errorf("mcp server %q not found", name)
	}
	if !enabled {
		return "", "", fmt.Errorf("mcp server %q is disabled", name)
	}
	if len(sealed) > 0 {
		raw, err := v.Decrypt(sealed)
		if err != nil {
			return "", "", fmt.Errorf("mcp server %q: decrypt failed", name)
		}
		authHeader = string(raw)
	}
	return url, authHeader, nil
}
