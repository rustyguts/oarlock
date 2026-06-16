package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ContainerRun runs an arbitrary container as a step. Execute submits the
// container and suspends (freeing the worker slot); Resume polls and finalizes.
// Execution strategy (local Docker vs k8s Jobs) lives entirely in the injected
// ContainerRuntime — the engine never learns which (hard rule 5).
type ContainerRun struct{ svc *Services }

const (
	containerPollMin = 2 * time.Second
	containerPollMax = 30 * time.Second
)

func (e *ContainerRun) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	spec, err := e.buildSpec(ctx, in)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("container.run: %w", err)
	}
	h, err := e.svc.Container.Submit(ctx, spec)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("container.run: submit: %w", err)
	}
	in.Log.Info("container submitted", "image", spec.Image, "compute_target", spec.Backend)
	return TaskOutput{}, SuspendNow("poll", time.Now().Add(containerPollMin), map[string]any{
		"handle": map[string]any(h),
		"polls":  float64(0),
	})
}

func (e *ContainerRun) Resume(ctx context.Context, in TaskInput, s SuspensionState) (TaskOutput, error) {
	h := handleFromPayload(s.Payload)
	if h == nil {
		return TaskOutput{}, fmt.Errorf("container.run: lost container handle")
	}
	status, err := e.svc.Container.Poll(ctx, h)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("container.run: poll: %w", err)
	}
	if !status.Terminal() {
		polls := payloadFloat(s.Payload, "polls") + 1
		return TaskOutput{}, SuspendNow("poll", time.Now().Add(pollBackoff(polls)), map[string]any{
			"handle": map[string]any(h),
			"polls":  polls,
		})
	}

	res, rerr := e.svc.Container.Result(ctx, h)
	defer func() { _ = e.svc.Container.Cleanup(context.WithoutCancel(ctx), h) }()
	if rerr != nil {
		return TaskOutput{}, fmt.Errorf("container.run: result: %w", rerr)
	}

	// Mirror container stderr into the task log (redacted by the log handler).
	mirrorLines(in.Log, res.StderrTail)
	if e.svc.Meter != nil {
		_ = e.svc.Meter.RecordContainerSeconds(ctx, in, fmt.Sprint(in.Config["compute_target"]),
			fmt.Sprint(in.Config["image"]), res.DurationSeconds())
	}

	out := map[string]any{
		"exit_code": res.ExitCode,
		"stdout":    parseStdout(res.Stdout),
		"artifacts": res.Outputs,
	}
	if res.ExitCode != 0 {
		return TaskOutput{Data: out}, fmt.Errorf("container exited with code %d", res.ExitCode)
	}
	in.Log.Info("container finished", "exit_code", res.ExitCode, "artifacts", len(res.Outputs),
		"seconds", res.DurationSeconds())
	return TaskOutput{Data: out}, nil
}

// buildSpec resolves the compute target, image, command, env, input artifacts,
// registry creds, and clamped resource limits into a ContainerSpec.
func (e *ContainerRun) buildSpec(ctx context.Context, in TaskInput) (ContainerSpec, error) {
	var spec ContainerSpec
	if e.svc.Container == nil || e.svc.Artifacts == nil || e.svc.Compute == nil {
		return spec, fmt.Errorf("container runtime not configured")
	}

	targetName := strings.TrimSpace(asString(in.Config["compute_target"]))
	if targetName == "" {
		return spec, fmt.Errorf("compute_target is required")
	}
	target, err := e.svc.Compute.ComputeTarget(ctx, in.WorkspaceID, targetName)
	if err != nil {
		return spec, err
	}
	if !target.Enabled {
		return spec, fmt.Errorf("compute target %q is disabled", targetName)
	}
	if target.Backend != e.svc.Container.Backend() {
		return spec, fmt.Errorf("compute target %q uses backend %q but this engine runs %q",
			targetName, target.Backend, e.svc.Container.Backend())
	}

	image := strings.TrimSpace(asString(in.Config["image"]))
	if image == "" {
		return spec, fmt.Errorf("image is required")
	}
	if !imageAllowed(target.ImageAllowlist, image) {
		return spec, fmt.Errorf("image %q is not allowed by compute target %q", image, targetName)
	}

	command, err := toStrings(in.Config["command"])
	if err != nil {
		return spec, fmt.Errorf("command: %w", err)
	}
	args, err := toStrings(in.Config["args"])
	if err != nil {
		return spec, fmt.Errorf("args: %w", err)
	}

	env, err := toStringMap(in.Config["env"])
	if err != nil {
		return spec, fmt.Errorf("env: %w", err)
	}

	inputs, err := e.resolveInputs(ctx, in.WorkspaceID, in.Config["input_artifacts"])
	if err != nil {
		return spec, err
	}
	globs, err := toStrings(in.Config["output_globs"])
	if err != nil {
		return spec, fmt.Errorf("output_globs: %w", err)
	}

	var reg *RegistryAuth
	if target.RegistrySecret != "" {
		user, pass, err := e.svc.Secrets.Registry(ctx, in.WorkspaceID, target.RegistrySecret)
		if err != nil {
			return spec, err
		}
		reg = &RegistryAuth{Username: user, Password: pass}
	}

	cpu := strings.TrimSpace(asString(in.Config["cpu"]))
	if cpu == "" {
		cpu = target.CPULimit
	}
	mem := clampInt(toInt(in.Config["memory_mb"]), target.MemoryMBLimit)
	timeout := clampInt(toInt(in.Config["timeout_sec"]), target.TimeoutSecLimit)

	return ContainerSpec{
		WorkspaceID: in.WorkspaceID, RunID: in.RunID, TaskID: in.TaskID, StepKey: in.StepKey,
		Image: image, Command: command, Args: args, Env: env,
		CPU: cpu, MemoryMB: mem, TimeoutSec: timeout,
		Inputs: inputs, OutputGlobs: globs, Registry: reg,
		Backend: target.Backend, Namespace: target.Namespace, RuntimeClass: target.RuntimeClass,
	}, nil
}

type inputArtifactSpec struct {
	From string `json:"from"` // already-interpolated artifact id
	As   string `json:"as"`   // filename under /oarlock/in
}

func (e *ContainerRun) resolveInputs(ctx context.Context, ws uuid.UUID, raw any) ([]ArtifactMount, error) {
	specs, err := parseInputArtifacts(raw)
	if err != nil {
		return nil, fmt.Errorf("input_artifacts: %w", err)
	}
	var mounts []ArtifactMount
	for _, s := range specs {
		id, err := uuid.Parse(strings.TrimSpace(s.From))
		if err != nil {
			return nil, fmt.Errorf("input_artifacts: %q is not an artifact id: %w", s.From, err)
		}
		ref, err := e.svc.Artifacts.Lookup(ctx, ws, id)
		if err != nil {
			return nil, err
		}
		dest := strings.TrimSpace(s.As)
		if dest == "" {
			dest = ref.Name
		}
		mounts = append(mounts, ArtifactMount{ArtifactID: ref.ID, Key: ref.Key, DestName: dest, Size: ref.Size})
	}
	return mounts, nil
}

func parseInputArtifacts(raw any) ([]inputArtifactSpec, error) {
	switch t := raw.(type) {
	case nil:
		return nil, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		var out []inputArtifactSpec
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		var out []inputArtifactSpec
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// --- small helpers ---

func handleFromPayload(payload map[string]any) Handle {
	if payload == nil {
		return nil
	}
	if m, ok := payload["handle"].(map[string]any); ok {
		return Handle(m)
	}
	return nil
}

func payloadFloat(payload map[string]any, k string) float64 {
	if payload == nil {
		return 0
	}
	if v, ok := payload[k].(float64); ok {
		return v
	}
	return 0
}

func pollBackoff(polls float64) time.Duration {
	d := containerPollMin << int(polls)
	if d > containerPollMax || d <= 0 {
		d = containerPollMax
	}
	return d
}

func parseStdout(b []byte) any {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return ""
	}
	var parsed any
	if json.Unmarshal(trimmed, &parsed) == nil {
		return parsed
	}
	return string(b)
}

func mirrorLines(log *slog.Logger, data []byte) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			log.Info(line)
		}
	}
}

func imageAllowed(allowlist []string, image string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, p := range allowlist {
		if strings.HasPrefix(image, p) {
			return true
		}
	}
	return false
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func toStrings(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprint(e))
		}
		return out, nil
	case []string:
		return t, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		if strings.HasPrefix(s, "[") {
			var arr []string
			if err := json.Unmarshal([]byte(s), &arr); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return []string{s}, nil
	default:
		return nil, fmt.Errorf("expected string or array, got %T", v)
	}
}

func toStringMap(v any) (map[string]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		out := make(map[string]string, len(t))
		for k, val := range t {
			out[k] = fmt.Sprint(val)
		}
		return out, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, err
		}
		return m, nil
	default:
		return nil, fmt.Errorf("expected JSON object, got %T", v)
	}
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n := 0
		_, _ = fmt.Sscanf(strings.TrimSpace(t), "%d", &n)
		return n
	}
	return 0
}

// clampInt returns requested if 0 < requested <= max, else max (the ceiling).
func clampInt(requested, max int) int {
	if requested <= 0 || requested > max {
		return max
	}
	return requested
}
