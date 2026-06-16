// Package steps defines the Executor interface (design §4.2) and the
// step-type registry. Execution strategy is a property of the step type,
// invisible to the engine; in-process is the default forever.
package steps

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

type TaskInput struct {
	WorkspaceID uuid.UUID
	RunID       uuid.UUID
	TaskID      uuid.UUID
	StepKey     string
	Config      map[string]any // post-interpolation
	Context     map[string]any // {"input": runInput, "steps": {key: output}}
	Log         *slog.Logger
}

type TaskOutput struct {
	Data any
}

type Executor interface {
	Execute(ctx context.Context, in TaskInput) (TaskOutput, error)
}

// TypeInfo describes a step type for API consumers (palette, property panels).
type TypeInfo struct {
	Type        string      `json:"type"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	ConfigSpec  []ConfigKey `json:"config_spec"`
}

type ConfigKey struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Kind        string   `json:"kind"` // string|text|number|select|rules|api_key|mcp_server|mcp_tool|compute_target
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Required    bool     `json:"required,omitempty"`
	// VisibleWhen optionally gates this field on another field's value, e.g.
	// {"mode": "rules"} shows the field only when config.mode == "rules". Empty
	// means always visible. Consumed by the Inspector UI.
	VisibleWhen map[string]string `json:"visible_when,omitempty"`
}

type Registry struct {
	executors map[string]Executor
	infos     []TypeInfo
}

func NewRegistry() *Registry {
	return &Registry{executors: map[string]Executor{}}
}

func (r *Registry) Register(info TypeInfo, e Executor) {
	r.executors[info.Type] = e
	r.infos = append(r.infos, info)
}

func (r *Registry) Get(stepType string) (Executor, bool) {
	e, ok := r.executors[stepType]
	return e, ok
}

func (r *Registry) Has(stepType string) bool {
	_, ok := r.executors[stepType]
	return ok
}

func (r *Registry) Types() []TypeInfo {
	return r.infos
}

// Default returns the registry with all native step types. svc provides the
// workspace-scoped secret resolvers for ai.* and mcp.* steps.
func Default(svc *Services) *Registry {
	r := NewRegistry()
	r.Register(TypeInfo{
		Type: "ai.prompt", Label: "AI Prompt",
		Description: "Send a prompt to an LLM via your own API key (Anthropic, OpenAI, OpenRouter)",
		ConfigSpec: []ConfigKey{
			{Key: "api_key", Label: "API key", Kind: "api_key", Required: true},
			{Key: "model", Label: "Model", Kind: "string", Placeholder: "claude-sonnet-4-6 / gpt-4o / openrouter model id", Required: true},
			{Key: "system", Label: "System prompt", Kind: "text", Placeholder: "You are a helpful assistant…"},
			{Key: "prompt", Label: "Prompt", Kind: "text", Placeholder: "Summarize: {{steps.fetch.body}}", Required: true},
			{Key: "max_tokens", Label: "Max tokens", Kind: "number", Placeholder: "1024"},
		},
	}, &AIPrompt{svc: svc})
	r.Register(TypeInfo{
		Type: "mcp.tool", Label: "MCP Tool",
		Description: "Call a tool on one of this workspace's connections",
		ConfigSpec: []ConfigKey{
			{Key: "server", Label: "Connection", Kind: "mcp_server", Required: true},
			{Key: "tool", Label: "Tool", Kind: "mcp_tool", Required: true},
			{Key: "arguments", Label: "Arguments (JSON)", Kind: "text", Placeholder: `{"query": "{{input.q}}"}`},
		},
	}, &MCPTool{svc: svc})
	r.Register(TypeInfo{
		Type: "http.request", Label: "HTTP Request",
		Description: "Call an HTTP endpoint and return the response",
		ConfigSpec: []ConfigKey{
			{Key: "url", Label: "URL", Kind: "string", Placeholder: "https://api.example.com/...", Required: true},
			{Key: "method", Label: "Method", Kind: "select", Options: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
			{Key: "body", Label: "Body", Kind: "text", Placeholder: `{"hello": "world"}`},
			{Key: "headers", Label: "Headers (JSON)", Kind: "text", Placeholder: `{"Authorization": "Bearer ..."}`},
		},
	}, &HTTPRequest{})
	r.Register(TypeInfo{
		Type: "transform", Label: "Transform (JS)",
		Description: "Run a JavaScript expression over prior step outputs",
		ConfigSpec: []ConfigKey{
			{Key: "script", Label: "Script", Kind: "text", Placeholder: "return steps.fetch.body.items.length", Required: true},
		},
	}, &Transform{})
	r.Register(TypeInfo{
		Type: "delay", Label: "Delay",
		Description: "Wait a fixed duration before continuing",
		ConfigSpec: []ConfigKey{
			{Key: "seconds", Label: "Seconds", Kind: "number", Placeholder: "5", Required: true},
		},
	}, &Delay{})
	// "condition" must match definition.ConditionType (kept literal here so the
	// steps package doesn't depend on definition).
	r.Register(TypeInfo{
		Type: "condition", Label: "Condition (If/Else)",
		Description: "Branch the workflow: evaluate rules (or a JS expression) and route to the Then or Else path",
		ConfigSpec: []ConfigKey{
			{Key: "mode", Label: "Mode", Kind: "select", Options: []string{"rules", "expression"}},
			{Key: "combinator", Label: "Match", Kind: "select", Options: []string{"and", "or"}, VisibleWhen: map[string]string{"mode": "rules"}},
			{Key: "rules", Label: "Rules", Kind: "rules", VisibleWhen: map[string]string{"mode": "rules"}},
			{Key: "expression", Label: "Expression (JS)", Kind: "text", Placeholder: "steps.fetch.body.count > 0", VisibleWhen: map[string]string{"mode": "expression"}},
		},
	}, &Condition{})
	// container.run is registered only when a container runtime + artifact store
	// are configured (OARLOCK_CONTAINER_RUNTIME + object store). When absent it
	// stays out of /v1/step-types and the palette — clean degradation.
	if svc != nil && svc.Container != nil && svc.Artifacts != nil {
		r.Register(TypeInfo{
			Type: "container.run", Label: "Run Container",
			Description: "Run any Docker image (e.g. ffprobe, ffmpeg) with files staged in and out",
			ConfigSpec: []ConfigKey{
				{Key: "compute_target", Label: "Compute target", Kind: "compute_target", Required: true},
				{Key: "image", Label: "Image", Kind: "string", Placeholder: "linuxserver/ffmpeg:latest", Required: true},
				{Key: "command", Label: "Command (JSON array)", Kind: "text", Placeholder: `["ffprobe","-v","quiet"]`},
				{Key: "args", Label: "Args (JSON array)", Kind: "text", Placeholder: `["-print_format","json","-show_format","/oarlock/in/video.mp4"]`},
				{Key: "env", Label: "Environment (JSON object)", Kind: "text", Placeholder: `{"LOG_LEVEL":"info","TOKEN":"{{secrets.my_token}}"}`},
				{Key: "input_artifacts", Label: "Input artifacts (JSON)", Kind: "text", Placeholder: `[{"from":"{{steps.upload.artifacts[0].id}}","as":"video.mp4"}]`},
				{Key: "output_globs", Label: "Output files (JSON array of globs)", Kind: "text", Placeholder: `["*.mp4","*.json"]`},
				{Key: "cpu", Label: "CPU", Kind: "string", Placeholder: "1"},
				{Key: "memory_mb", Label: "Memory (MB)", Kind: "number", Placeholder: "1024"},
				{Key: "timeout_sec", Label: "Timeout (s)", Kind: "number", Placeholder: "300"},
			},
		}, &ContainerRun{svc: svc})
	}
	return r
}
