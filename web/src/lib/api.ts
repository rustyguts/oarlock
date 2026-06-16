import { env } from '$env/dynamic/public';
import { browser } from '$app/environment';

// The browser talks to the Go API directly (CORS is open in dev).
const BASE = (browser && env.PUBLIC_API_URL) || env.PUBLIC_API_URL || 'http://localhost:9000';

export interface StepUI {
	x: number;
	y: number;
}

export interface Step {
	key: string;
	type: string;
	needs?: string[];
	// Maps a condition predecessor key → the branch ("then"/"else") this step is
	// wired to. Derived from the canvas edge's sourceHandle.
	branches?: Record<string, string>;
	config?: Record<string, unknown>;
	retries?: number;
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
	kind:
		| 'string'
		| 'text'
		| 'number'
		| 'select'
		| 'rules'
		| 'api_key'
		| 'mcp_server'
		| 'mcp_tool'
		| 'compute_target';
	options?: string[];
	placeholder?: string;
	required?: boolean;
	// Show this field only when another config field equals a value, e.g.
	// { mode: 'rules' }. Empty/absent = always visible.
	visible_when?: Record<string, string>;
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
	workspace: { id: string; name: string };
	role: string;
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
	listRuns: (id: string) => req<RunSummary[]>(`/v1/workflows/${id}/runs`),
	getRun: (id: string) => req<Run>(`/v1/runs/${id}`),
	cancelRun: (id: string) => req<{ status: string }>(`/v1/runs/${id}/cancel`, { method: 'POST' }),
	retryRun: (id: string) => req<{ status: string }>(`/v1/runs/${id}/retry`, { method: 'POST' }),
	runLogs: (id: string) => req<LogLine[]>(`/v1/runs/${id}/logs`),
	// Secrets (Configuration page): generic values, BYOK api_key, registry creds
	listSecrets: () => req<Secret[]>('/v1/secrets'),
	createSecret: (c: {
		name: string;
		type: string;
		provider?: string;
		value?: string;
		username?: string;
		password?: string;
	}) => req<{ id: string }>('/v1/secrets', { method: 'POST', body: JSON.stringify(c) }),
	deleteSecret: (id: string) => req<void>(`/v1/secrets/${id}`, { method: 'DELETE' }),
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
	// Compute targets (for container.run steps)
	listComputeTargets: () => req<ComputeTarget[]>('/v1/compute-targets'),
	createComputeTarget: (t: ComputeTargetInput) =>
		req<{ id: string }>('/v1/compute-targets', { method: 'POST', body: JSON.stringify(t) }),
	updateComputeTarget: (id: string, t: ComputeTargetInput) =>
		req<{ id: string }>(`/v1/compute-targets/${id}`, { method: 'PUT', body: JSON.stringify(t) }),
	deleteComputeTarget: (id: string) =>
		req<void>(`/v1/compute-targets/${id}`, { method: 'DELETE' })
};

export interface Secret {
	id: string;
	name: string;
	type: 'generic' | 'api_key' | 'registry';
	provider?: string;
	value_hint: string;
	created_at: string;
}

export interface ComputeTarget {
	id: string;
	name: string;
	backend: 'docker' | 'k8s';
	namespace?: string;
	runtime_class?: string;
	cpu_limit: string;
	memory_mb_limit: number;
	timeout_sec_limit: number;
	image_allowlist: string[];
	registry_secret_name?: string;
	is_enabled: boolean;
	created_at: string;
	updated_at: string;
}

export interface ComputeTargetInput {
	name: string;
	backend: string;
	namespace?: string;
	runtime_class?: string;
	cpu_limit?: string;
	memory_mb_limit?: number;
	timeout_sec_limit?: number;
	image_allowlist?: string[];
	registry_secret_name?: string;
	is_enabled?: boolean;
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

// Subscribe to live run snapshots over SSE. The server closes the stream
// after sending the terminal snapshot. Returns an unsubscribe function.
export function watchRun(
	id: string,
	onRun: (run: Run) => void,
	onError?: (err: Error) => void
): () => void {
	const source = new EventSource(`${BASE}/v1/runs/${id}/events`, { withCredentials: true });
	source.addEventListener('run', (e) => {
		const run: Run = JSON.parse((e as MessageEvent).data);
		onRun(run);
		if (['succeeded', 'failed', 'canceled'].includes(run.status)) source.close();
	});
	source.onerror = () => {
		// EventSource auto-reconnects; only surface when permanently closed.
		if (source.readyState === EventSource.CLOSED) onError?.(new Error('event stream closed'));
	};
	return () => source.close();
}
