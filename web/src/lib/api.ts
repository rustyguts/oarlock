import { env } from '$env/dynamic/public';

// The browser talks to the Go API directly (CORS is open in dev).
const BASE = env.PUBLIC_API_URL || 'http://localhost:9000';

// Exposed so callers can build off-API URLs (e.g. the webhook ingress URL,
// which lives under /hooks, not /v1).
export const API_BASE = BASE;

export interface StepUI {
	x: number;
	y: number;
}

export interface Step {
	key: string;
	type: string;
	needs?: string[];
	config?: Record<string, unknown>;
	retries?: number;
	timeout?: number; // per-step execution timeout in seconds (0–600); optional
	// JS expression gating this step: falsy → the step is skipped at runtime.
	// Sees `input` and upstream step outputs; no secrets. Empty = always run.
	if?: string;
	ui?: StepUI;
}

export interface Definition {
	name?: string;
	steps: Step[];
}

export interface Workflow {
	id: string;
	name: string;
	slug: string;
	is_enabled: boolean;
	version: number | null;
	run_count: number;
	failed_count: number;
	definition?: Definition;
	created_at: string;
	updated_at: string;
}

export interface ConfigKey {
	key: string;
	label: string;
	kind: 'string' | 'text' | 'number' | 'select' | 'api_key' | 'mcp_server' | 'mcp_tool';
	options?: string[];
	placeholder?: string;
	required?: boolean;
}

export interface StepType {
	type: string;
	label: string;
	description: string;
	config_spec: ConfigKey[];
}

export interface TaskInfo {
	id: string;
	step_key: string;
	attempt: number;
	status: string;
	output: unknown;
	error: { message?: string } | null;
	queued_at: string;
	started_at: string | null;
	finished_at: string | null;
}

export interface Run {
	id: string;
	workflow_id: string;
	workflow_name: string;
	version: number;
	definition: Definition; // pinned version this run executed
	status: string;
	input: unknown;
	error: unknown;
	created_at: string;
	started_at: string | null;
	finished_at: string | null;
	tasks: TaskInfo[];
}

export interface RunSummary {
	id: string;
	status: string;
	created_at: string;
	started_at: string | null;
	finished_at: string | null;
	version: number;
	task_count: number;
	error_summary: string | null;
}

// A saved, immutable version of a workflow's definition (design glossary: Version).
export interface WorkflowVersion {
	version: number;
	created_at: string;
	step_count: number;
	is_current: boolean;
}

export interface WorkflowVersionDetail {
	version: number;
	created_at: string;
	definition: Definition;
}

// A workflow trigger (design glossary: Trigger). Schedule triggers fire on a
// 5-field cron; webhook triggers fire on POST /hooks/{workspace}/{path}.
export interface ScheduleTriggerConfig {
	cron: string;
}
export interface WebhookTriggerConfig {
	path: string;
	secret?: string;
}

export interface Trigger {
	id: string;
	type: 'schedule' | 'webhook';
	config: ScheduleTriggerConfig | WebhookTriggerConfig;
	is_enabled: boolean;
	created_at: string;
}

// A workspace API token used to authenticate an MCP client (design: MCP Access).
// The raw token is returned exactly once at creation; afterwards only a masked
// prefix is ever exposed.
export interface ApiToken {
	id: string;
	name: string;
	prefix: string;
	created_at: string;
	last_used_at: string | null;
}

// ApiError carries the response payload — e.g. the list of workflows that
// block a delete (409).
export class ApiError extends Error {
	status: number;
	workflows?: string[];
	constructor(status: number, message: string, workflows?: string[]) {
		super(message);
		this.status = status;
		this.workflows = workflows;
	}
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(`${BASE}${path}`, {
		headers: { 'Content-Type': 'application/json' },
		credentials: 'include', // session cookie (auto-login bootstrap)
		...init
	});
	if (!res.ok) {
		let message = `${res.status} ${res.statusText}`;
		let workflows: string[] | undefined;
		try {
			const body = await res.json();
			if (body.error) message = body.error;
			if (Array.isArray(body.workflows)) workflows = body.workflows;
		} catch {
			/* keep default message */
		}
		throw new ApiError(res.status, message, workflows);
	}
	if (res.status === 204) return undefined as T;
	return res.json();
}

export interface Me {
	user: { id: string; email: string; name: string | null };
	workspace: { id: string; name: string; slug?: string };
	role: string;
	// Present on newer API builds; absent on older ones — treat missing as "not dev".
	vault?: { dev_key: boolean };
}

export interface Stats {
	totals: {
		workflows: number;
		runs: number;
		succeeded: number;
		failed: number;
		canceled: number;
		active: number;
		tasks: number;
		log_lines: number;
		secrets: number;
		mcp_servers: number;
		avg_duration_ms: number | null;
		success_rate: number | null;
	};
	daily: { date: string; succeeded: number; failed: number; canceled: number }[];
	top_workflows: {
		id: string;
		name: string;
		runs: number;
		failed: number;
		avg_duration_ms: number | null;
		last_run_at: string | null;
	}[];
	recent_runs: {
		id: string;
		workflow_id: string;
		workflow_name: string;
		status: string;
		created_at: string;
		started_at: string | null;
		finished_at: string | null;
	}[];
	task_statuses: Record<string, number>;
}

export const api = {
	me: () => req<Me>('/v1/me'),
	stats: () => req<Stats>('/v1/stats'),
	stepTypes: () => req<StepType[]>('/v1/step-types'),
	listWorkflows: () => req<Workflow[]>('/v1/workflows'),
	createWorkflow: (name: string) =>
		req<{ id: string; version: number }>('/v1/workflows', {
			method: 'POST',
			body: JSON.stringify({ name })
		}),
	getWorkflow: (id: string) => req<Workflow>(`/v1/workflows/${id}`),
	deleteWorkflow: (id: string) => req<void>(`/v1/workflows/${id}`, { method: 'DELETE' }),
	// Rename and/or toggle trigger-gating. is_enabled gates future triggers only;
	// manual runs are always allowed.
	patchWorkflow: (id: string, patch: { name?: string; is_enabled?: boolean }) =>
		req<{ id: string; name: string; is_enabled: boolean }>(`/v1/workflows/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(patch)
		}),
	// Immutable version history (design glossary: Version).
	listVersions: (id: string) => req<WorkflowVersion[]>(`/v1/workflows/${id}/versions`),
	getVersion: (id: string, version: number) =>
		req<WorkflowVersionDetail>(`/v1/workflows/${id}/versions/${version}`),
	saveDefinition: (id: string, definition: Definition) =>
		req<{ id: string; version: number }>(`/v1/workflows/${id}/definition`, {
			method: 'PUT',
			body: JSON.stringify({ definition })
		}),
	startRun: (id: string, input: Record<string, unknown> = {}) =>
		req<{ run_id: string }>(`/v1/workflows/${id}/runs`, {
			method: 'POST',
			body: JSON.stringify({ input })
		}),
	// Triggers (schedule / webhook). Referenced by workflow; gated by is_enabled.
	listTriggers: (id: string) => req<Trigger[]>(`/v1/workflows/${id}/triggers`),
	createTrigger: (id: string, t: { type: string; config: unknown; is_enabled?: boolean }) =>
		req<{ id: string }>(`/v1/workflows/${id}/triggers`, {
			method: 'POST',
			body: JSON.stringify(t)
		}),
	patchTrigger: (id: string, patch: { config?: unknown; is_enabled?: boolean }) =>
		req<{ id: string }>(`/v1/triggers/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
	deleteTrigger: (id: string) => req<void>(`/v1/triggers/${id}`, { method: 'DELETE' }),
	// Cursor-paginated, newest first. `before` = the last-seen run id; a page
	// shorter than `limit` is the final page.
	listRuns: (id: string, opts?: { limit?: number; before?: string }) => {
		const q = new URLSearchParams();
		if (opts?.limit != null) q.set('limit', String(opts.limit));
		if (opts?.before) q.set('before', opts.before);
		const qs = q.toString();
		return req<RunSummary[]>(`/v1/workflows/${id}/runs${qs ? `?${qs}` : ''}`);
	},
	getRun: (id: string) => req<Run>(`/v1/runs/${id}`),
	cancelRun: (id: string) => req<{ status: string }>(`/v1/runs/${id}/cancel`, { method: 'POST' }),
	retryRun: (id: string) => req<{ status: string }>(`/v1/runs/${id}/retry`, { method: 'POST' }),
	// Cursor-paginated, newest first. `before_id` = the last-seen numeric log id.
	runLogs: (id: string, opts?: { limit?: number; before_id?: number }) => {
		const q = new URLSearchParams();
		if (opts?.limit != null) q.set('limit', String(opts.limit));
		if (opts?.before_id != null) q.set('before_id', String(opts.before_id));
		const qs = q.toString();
		return req<LogLine[]>(`/v1/runs/${id}/logs${qs ? `?${qs}` : ''}`);
	},
	// API tokens (MCP Access page). Create returns the raw token once.
	listApiTokens: () => req<ApiToken[]>('/v1/api-tokens'),
	createApiToken: (name: string) =>
		req<{ id: string; token: string }>('/v1/api-tokens', {
			method: 'POST',
			body: JSON.stringify({ name })
		}),
	deleteApiToken: (id: string) => req<void>(`/v1/api-tokens/${id}`, { method: 'DELETE' }),
	// Secrets (Configuration page): generic values + BYOK api_key type
	listSecrets: () => req<Secret[]>('/v1/secrets'),
	createSecret: (c: { name: string; type: string; provider?: string; value: string }) =>
		req<{ id: string }>('/v1/secrets', { method: 'POST', body: JSON.stringify(c) }),
	deleteSecret: (id: string) => req<void>(`/v1/secrets/${id}`, { method: 'DELETE' }),
	// Rotate a secret's value in place (write-only; keeps the same id/name).
	rotateSecret: (id: string, value: string) =>
		req<{ id: string }>(`/v1/secrets/${id}`, { method: 'PUT', body: JSON.stringify({ value }) }),
	// MCP servers
	listMCPServers: () => req<MCPServer[]>('/v1/mcp-servers'),
	createMCPServer: (m: { name: string; url: string; auth_header?: string }) =>
		req<{ id: string }>('/v1/mcp-servers', { method: 'POST', body: JSON.stringify(m) }),
	updateMCPServer: (
		id: string,
		m: { name: string; url: string; auth_header?: string | null; is_enabled?: boolean }
	) => req<{ id: string }>(`/v1/mcp-servers/${id}`, { method: 'PUT', body: JSON.stringify(m) }),
	deleteMCPServer: (id: string) => req<void>(`/v1/mcp-servers/${id}`, { method: 'DELETE' }),
	mcpServerTools: (id: string) => req<MCPToolInfo[]>(`/v1/mcp-servers/${id}/tools`),
	// Stateless connection test — probes an endpoint without persisting a server.
	mcpTest: (m: { url: string; auth_header?: string }) =>
		req<MCPToolInfo[]>('/v1/mcp-servers/test', { method: 'POST', body: JSON.stringify(m) })
};

export interface Secret {
	id: string;
	name: string;
	type: 'generic' | 'api_key';
	provider?: string;
	value_hint: string;
	created_at: string;
}

export interface MCPServer {
	id: string;
	name: string;
	url: string;
	has_auth: boolean;
	is_enabled: boolean;
	created_at: string;
	updated_at: string;
}

export interface MCPToolInfo {
	name: string;
	description: string;
	input_schema: unknown;
}

export interface LogLine {
	id: number;
	task_id: string;
	step_key: string;
	ts: string;
	level: number; // slog: 0=info 4=warn 8=error
	message: string;
	fields: Record<string, unknown> | null;
}

export function runLogsDownloadUrl(id: string): string {
	return `${BASE}/v1/runs/${id}/logs.txt`;
}

// The workspace MCP endpoint. Point an MCP client here and authenticate with an
// API token (bearer). See the MCP Access page.
export function mcpUrl(): string {
	return `${BASE}/mcp`;
}

// Subscribe to live run snapshots over SSE. The server closes the stream
// after sending the terminal snapshot. Returns an unsubscribe function.
export function watchRun(
	id: string,
	onRun: (run: Run) => void,
	onError?: (err: Error) => void
): () => void {
	const source = new EventSource(`${BASE}/v1/runs/${id}/events`, { withCredentials: true });
	source.addEventListener('run', (e) => {
		let run: Run;
		try {
			run = JSON.parse((e as MessageEvent).data);
		} catch {
			// One malformed frame must not kill the listener; wait for the next.
			return;
		}
		onRun(run);
		if (['succeeded', 'failed', 'canceled'].includes(run.status)) source.close();
	});
	source.onerror = () => {
		// EventSource auto-reconnects; only surface when permanently closed.
		if (source.readyState === EventSource.CLOSED) onError?.(new Error('event stream closed'));
	};
	return () => source.close();
}
