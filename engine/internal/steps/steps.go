// Package steps defines the Executor interface (design §4.2) and the
// step-type registry. Execution strategy is a property of the step type,
// invisible to the engine; in-process is the default forever.
package steps

import (
	"context"
	"log/slog"
	"time"

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

// Suspend is the sentinel an Executor returns to park its task instead of
// finishing it (design §4.1 "Long waits"): the worker slot is freed and the
// task sits 'suspended' until a scheduled resume (delay) or an external HTTP
// callback (approval) revives it. The engine keys off it via errors.As, so it
// is returned as the error value — always as a *Suspend, since Error is on the
// pointer receiver. Kind ∈ delay|callback.
type Suspend struct {
	Kind     string     // delay|callback
	ResumeAt *time.Time // set for a timed resume; nil for callback-driven
	Output   any        // the task's output while suspended (e.g. the callback URL info)
}

func (s *Suspend) Error() string { return "suspend: " + s.Kind }

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
	Kind        string   `json:"kind"` // string|text|number|select
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Required    bool     `json:"required,omitempty"`
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
		Description: "Call a tool on one of this workspace's MCP servers",
		ConfigSpec: []ConfigKey{
			{Key: "server", Label: "MCP server", Kind: "mcp_server", Required: true},
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
		Type: "code.js", Label: "Code (JS)",
		Description: "Run JavaScript with console.log captured to the task log",
		ConfigSpec: []ConfigKey{
			{Key: "script", Label: "Script", Kind: "text", Placeholder: "const items = steps.fetch.body.items\nreturn items.filter(i => i.active)", Required: true},
		},
	}, &CodeJS{})
	r.Register(TypeInfo{
		Type: "delay", Label: "Delay",
		Description: "Wait a fixed duration before continuing; waits over 5 minutes suspend the run and resume on schedule (max 30 days)",
		ConfigSpec: []ConfigKey{
			{Key: "seconds", Label: "Seconds", Kind: "number", Placeholder: "5 (over 300 suspends, up to 30 days)", Required: true},
		},
	}, &Delay{})
	r.Register(TypeInfo{
		Type: "wait.callback", Label: "Wait for Callback",
		Description: "Suspend until an external HTTP callback resumes this step",
		ConfigSpec: []ConfigKey{
			{Key: "note", Label: "Note", Kind: "text", Placeholder: "Shown in the output while waiting (e.g. what to approve)"},
		},
	}, &WaitCallback{})
	return r
}
