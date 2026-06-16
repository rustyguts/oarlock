package vault

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// Registry resolves a registry-typed secret to container-registry credentials.
// The secret value is JSON {"username","password"}.
func (v *Vault) Registry(ctx context.Context, workspaceID uuid.UUID, name string) (username, password string, err error) {
	var sealed []byte
	err = v.Pool.QueryRow(ctx, `
		select encrypted_data from secrets
		where workspace_id = $1 and name = $2 and type = 'registry'`, workspaceID, name).Scan(&sealed)
	if err != nil {
		return "", "", fmt.Errorf("registry secret %q not found", name)
	}
	val, err := v.openSecret(name, sealed)
	if err != nil {
		return "", "", err
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(val), &creds); err != nil {
		return "", "", fmt.Errorf("registry secret %q: invalid format (expected {username,password})", name)
	}
	return creds.Username, creds.Password, nil
}

// ComputeTarget resolves a named compute target for the container executor.
func (v *Vault) ComputeTarget(ctx context.Context, workspaceID uuid.UUID, name string) (steps.ComputeTarget, error) {
	var t steps.ComputeTarget
	var ns, rc, regSecret *string
	err := v.Pool.QueryRow(ctx, `
		select name, backend, namespace, runtime_class, cpu_limit, memory_mb_limit,
		       timeout_sec_limit, image_allowlist, registry_secret_name, is_enabled
		from compute_targets where workspace_id = $1 and name = $2`, workspaceID, name).
		Scan(&t.Name, &t.Backend, &ns, &rc, &t.CPULimit, &t.MemoryMBLimit,
			&t.TimeoutSecLimit, &t.ImageAllowlist, &regSecret, &t.Enabled)
	if err != nil {
		return t, fmt.Errorf("compute target %q not found", name)
	}
	if ns != nil {
		t.Namespace = *ns
	}
	if rc != nil {
		t.RuntimeClass = *rc
	}
	if regSecret != nil {
		t.RegistrySecret = *regSecret
	}
	return t, nil
}
