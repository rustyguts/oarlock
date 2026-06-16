package steps

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"
)

// In-container staging convention: input artifacts are materialized read-only
// under ContainerInputDir, and files the container writes under
// ContainerOutputDir are collected as output artifacts after it exits.
const (
	ContainerInputDir  = "/oarlock/in"
	ContainerOutputDir = "/oarlock/out"
)

// --- container runtime ---

// ContainerRuntime runs a single container to completion out-of-process. It is
// async-shaped: Submit starts the work and returns an opaque, serializable
// Handle; the engine suspends the task and later polls. The local Docker backend
// and the Kubernetes Jobs backend implement this identically — the engine never
// learns which (hard rule 5: execution strategy is a property of the step type).
type ContainerRuntime interface {
	// Backend names this runtime ("docker" | "k8s"); compute targets are
	// validated against it.
	Backend() string
	// Submit starts the container/Job and returns an opaque handle. Non-blocking.
	Submit(ctx context.Context, spec ContainerSpec) (Handle, error)
	// Poll reports current status without blocking on completion.
	Poll(ctx context.Context, h Handle) (ContainerStatus, error)
	// Result is valid once Poll reports terminal: exit code, captured stdout,
	// stderr tail, recorded output artifacts, and timing for metering.
	Result(ctx context.Context, h Handle) (ContainerResult, error)
	// Logs streams stdout+stderr produced since the given time (live mirroring).
	Logs(ctx context.Context, h Handle, since time.Time) (io.ReadCloser, error)
	// Cancel kills the container / deletes the Job. Idempotent.
	Cancel(ctx context.Context, h Handle) error
	// Cleanup removes the container, staging dirs, and per-job secret. Idempotent.
	Cleanup(ctx context.Context, h Handle) error
}

// Handle is an opaque, JSON-serializable pointer to a submitted container. It
// round-trips through the suspensions.payload column, so it holds only
// JSON-native values; each backend tags it with a "kind".
type Handle map[string]any

type ContainerPhase string

const (
	PhasePending   ContainerPhase = "pending"
	PhaseRunning   ContainerPhase = "running"
	PhaseSucceeded ContainerPhase = "succeeded"
	PhaseFailed    ContainerPhase = "failed"
)

type ContainerStatus struct {
	Phase    ContainerPhase
	ExitCode *int
	Message  string
}

func (s ContainerStatus) Terminal() bool {
	return s.Phase == PhaseSucceeded || s.Phase == PhaseFailed
}

// ContainerSpec is the fully-resolved request to run one container: image and
// command already interpolated, env already resolved from the vault, resource
// limits already clamped to the compute target.
type ContainerSpec struct {
	WorkspaceID uuid.UUID
	RunID       uuid.UUID
	TaskID      uuid.UUID
	StepKey     string

	Image   string
	Command []string          // entrypoint override (optional)
	Args    []string          // command args
	Env     map[string]string // vault-sourced; never placed on the command line

	CPU        string // "1" or "500m"
	MemoryMB   int
	TimeoutSec int

	Inputs      []ArtifactMount // staged read-only into /oarlock/in/<DestName>
	OutputGlobs []string        // filename globs collected from /oarlock/out (default ["*"])

	Registry *RegistryAuth

	// Placement, from the compute target.
	Backend      string
	Namespace    string
	RuntimeClass string
}

type ArtifactMount struct {
	ArtifactID uuid.UUID
	Key        string // object key, ws/{workspace_id}/...
	DestName   string // filename under /oarlock/in/
	Size       int64  // bytes (needed up front for the tar header)
}

type RegistryAuth struct {
	Username string
	Password string
}

type ContainerResult struct {
	ExitCode   int
	Stdout     []byte        // captured stdout (capped); redacted before persist
	StderrTail []byte        // last chunk of stderr (capped)
	Outputs    []ArtifactRef // uploaded + recorded output artifacts
	StartedAt  time.Time
	FinishedAt time.Time
}

func (r ContainerResult) DurationSeconds() float64 {
	if r.FinishedAt.IsZero() || r.StartedAt.IsZero() {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt).Seconds()
}

// --- artifact store ---

// ArtifactRef is the downstream-visible handle to an artifact, surfaced under
// steps.<key>.artifacts. Key is internal (json:"-"), never exposed to
// expressions or the API.
type ArtifactRef struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type"`
	Key         string    `json:"-"`
}

// ArtifactRecord is an output/upload to persist (metadata row + bytes already
// uploaded at Key).
type ArtifactRecord struct {
	WorkspaceID uuid.UUID
	RunID       *uuid.UUID
	TaskID      *uuid.UUID
	Key         string
	Name        string
	Size        int64
	ContentType string
	Source      string // "output" | "upload"
}

// ArtifactStore owns both artifact metadata rows and their object bytes
// (SeaweedFS in dev, R2 in prod). All object keys are prefixed ws/{workspace_id}/
// (hard rule 7); construct them only via OutputKey/UploadKey.
type ArtifactStore interface {
	Lookup(ctx context.Context, workspaceID, id uuid.UUID) (ArtifactRef, error)
	Record(ctx context.Context, rec ArtifactRecord) (ArtifactRef, error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Presign(ctx context.Context, method, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
	OutputKey(workspaceID, runID, taskID uuid.UUID, name string) string
	UploadKey(workspaceID, artifactID uuid.UUID, name string) string
}

// --- compute targets ---

type ComputeTargetSource interface {
	ComputeTarget(ctx context.Context, workspaceID uuid.UUID, name string) (ComputeTarget, error)
}

type ComputeTarget struct {
	Name            string
	Backend         string
	Namespace       string
	RuntimeClass    string
	CPULimit        string
	MemoryMBLimit   int
	TimeoutSecLimit int
	ImageAllowlist  []string
	RegistrySecret  string
	Enabled         bool
}

// --- metering ---

// Meter records billable usage. Only the container executor calls it — it is
// the only executor with real marginal cost (hard rule 8).
type Meter interface {
	RecordContainerSeconds(ctx context.Context, in TaskInput, computeTarget, image string, seconds float64) error
}
