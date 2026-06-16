import type { Page } from '@playwright/test';

// Deterministic API fixtures served via request interception — snapshot tests
// never touch the real backend. Frozen "now" in the specs is 09:05:00Z.

export const WF_ID = '11111111-1111-1111-1111-111111111111';
export const WF2_ID = '22222222-2222-2222-2222-222222222222';
export const WF3_ID = '33333333-3333-3333-3333-333333333333';
export const RUN_ID = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';
export const COND_RUN_ID = 'aaaaaaaa-cccc-cccc-cccc-aaaaaaaaaaaa';

// credentials: 'include' forbids a wildcard origin — echo the app origin.
const CORS = {
	'access-control-allow-origin': 'http://localhost:4173',
	'access-control-allow-credentials': 'true',
	'access-control-allow-methods': 'GET, POST, PUT, PATCH, DELETE, OPTIONS',
	'access-control-allow-headers': 'Content-Type, Authorization'
};

const DEFINITION = {
	name: 'order-sync',
	steps: [
		{
			key: 'fetch',
			type: 'http.request',
			config: { url: 'https://api.example.com/orders', method: 'GET' },
			ui: { x: 80, y: 200 }
		},
		{
			key: 'count',
			type: 'transform',
			needs: ['fetch'],
			config: { script: 'return steps.fetch.body.orders.length' },
			ui: { x: 360, y: 120 }
		},
		{
			key: 'title',
			type: 'transform',
			needs: ['fetch'],
			config: { script: 'return steps.fetch.body.title' },
			ui: { x: 360, y: 280 }
		},
		{
			key: 'report',
			type: 'transform',
			needs: ['count', 'title'],
			retries: 2,
			config: { script: 'return `${steps.title}: ${steps.count} orders`' },
			ui: { x: 640, y: 200 }
		}
	]
};

// A dedicated branching demo (separate from DEFINITION so the existing
// node-count waits stay valid): fetch → gate (condition) → ship (then) / hold (else).
const COND_DEFINITION = {
	name: 'release-gate',
	steps: [
		{
			key: 'fetch',
			type: 'http.request',
			config: { url: 'https://api.example.com/build', method: 'GET' },
			ui: { x: 80, y: 200 }
		},
		{
			key: 'gate',
			type: 'condition',
			needs: ['fetch'],
			config: {
				mode: 'rules',
				combinator: 'and',
				rules: [{ operand: 'steps.fetch.body.passed', operator: '==', value: 'true', kind: 'boolean' }]
			},
			ui: { x: 360, y: 200 }
		},
		{
			key: 'ship',
			type: 'http.request',
			needs: ['gate'],
			branches: { gate: 'then' },
			config: { url: 'https://api.example.com/ship', method: 'POST' },
			ui: { x: 640, y: 110 }
		},
		{
			key: 'hold',
			type: 'http.request',
			needs: ['gate'],
			branches: { gate: 'else' },
			config: { url: 'https://api.example.com/hold', method: 'POST' },
			ui: { x: 640, y: 300 }
		}
	]
};

const COND_WORKFLOW_DETAIL = {
	id: WF3_ID,
	name: 'Release Gate',
	slug: 'release-gate',
	is_enabled: true,
	version: 2,
	run_count: 3,
	failed_count: 0,
	definition: COND_DEFINITION,
	created_at: '2026-06-09 08:00:00+00',
	updated_at: '2026-06-11 12:00:00+00'
};

const COND_RUN_DETAIL = {
	id: COND_RUN_ID,
	workflow_id: WF3_ID,
	workflow_name: 'Release Gate',
	version: 2,
	definition: COND_DEFINITION,
	status: 'succeeded',
	input: null,
	error: null,
	created_at: '2026-06-12 08:45:00+00',
	started_at: '2026-06-12 08:45:00.1+00',
	finished_at: '2026-06-12 08:45:02.0+00',
	tasks: [
		{
			id: 'cccccccc-1111-0000-0000-000000000001',
			step_key: 'fetch',
			attempt: 1,
			status: 'succeeded',
			output: { status: 200, body: { passed: true } },
			error: null,
			queued_at: '2026-06-12 08:45:00.1+00',
			started_at: '2026-06-12 08:45:00.2+00',
			finished_at: '2026-06-12 08:45:00.5+00'
		},
		{
			id: 'cccccccc-1111-0000-0000-000000000002',
			step_key: 'gate',
			attempt: 1,
			status: 'succeeded',
			output: { result: true, branch: 'then' },
			error: null,
			queued_at: '2026-06-12 08:45:00.6+00',
			started_at: '2026-06-12 08:45:00.65+00',
			finished_at: '2026-06-12 08:45:00.7+00'
		},
		{
			id: 'cccccccc-1111-0000-0000-000000000003',
			step_key: 'ship',
			attempt: 1,
			status: 'succeeded',
			output: { status: 200, body: { shipped: true } },
			error: null,
			queued_at: '2026-06-12 08:45:00.8+00',
			started_at: '2026-06-12 08:45:00.85+00',
			finished_at: '2026-06-12 08:45:01.9+00'
		},
		{
			id: 'cccccccc-1111-0000-0000-000000000004',
			step_key: 'hold',
			attempt: 1,
			status: 'skipped',
			output: null,
			error: null,
			queued_at: '2026-06-12 08:45:00.7+00',
			started_at: null,
			finished_at: '2026-06-12 08:45:00.7+00'
		}
	]
};

const COND_RUNS = [
	{
		id: COND_RUN_ID,
		status: 'succeeded',
		created_at: '2026-06-12 08:45:00+00',
		started_at: '2026-06-12 08:45:00.1+00',
		finished_at: '2026-06-12 08:45:02.0+00',
		version: 2,
		task_count: 4,
		error_summary: null
	}
];

const STATS = {
	totals: {
		workflows: 2, runs: 47, succeeded: 37, failed: 9, canceled: 1, active: 0,
		tasks: 139, log_lines: 274, secrets: 2, mcp_servers: 2,
		avg_duration_ms: 4100, success_rate: 0.787
	},
	daily: [
		{ date: '2026-05-30', succeeded: 5, failed: 1, canceled: 0 },
		{ date: '2026-05-31', succeeded: 2, failed: 0, canceled: 0 },
		{ date: '2026-06-01', succeeded: 1, failed: 0, canceled: 0 },
		{ date: '2026-06-02', succeeded: 1, failed: 0, canceled: 0 },
		{ date: '2026-06-03', succeeded: 0, failed: 0, canceled: 0 },
		{ date: '2026-06-04', succeeded: 5, failed: 0, canceled: 0 },
		{ date: '2026-06-05', succeeded: 1, failed: 1, canceled: 0 },
		{ date: '2026-06-06', succeeded: 3, failed: 1, canceled: 0 },
		{ date: '2026-06-07', succeeded: 3, failed: 0, canceled: 0 },
		{ date: '2026-06-08', succeeded: 2, failed: 1, canceled: 0 },
		{ date: '2026-06-09', succeeded: 5, failed: 0, canceled: 0 },
		{ date: '2026-06-10', succeeded: 3, failed: 0, canceled: 0 },
		{ date: '2026-06-11', succeeded: 1, failed: 0, canceled: 0 },
		{ date: '2026-06-12', succeeded: 5, failed: 5, canceled: 1 }
	],
	top_workflows: [
		{ id: WF_ID, name: 'Order Sync', runs: 31, failed: 2, avg_duration_ms: 657, last_run_at: '2026-06-12 08:30:00+00' },
		{ id: WF2_ID, name: 'Daily Report', runs: 7, failed: 7, avg_duration_ms: 23600, last_run_at: '2026-06-12 08:00:00+00' }
	],
	recent_runs: [
		{ id: RUN_ID, workflow_id: WF_ID, workflow_name: 'Order Sync', status: 'failed', created_at: '2026-06-12 09:00:00+00', started_at: '2026-06-12 09:00:00.1+00', finished_at: '2026-06-12 09:00:05.4+00' },
		{ id: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb', workflow_id: WF_ID, workflow_name: 'Order Sync', status: 'succeeded', created_at: '2026-06-12 08:30:00+00', started_at: '2026-06-12 08:30:00.1+00', finished_at: '2026-06-12 08:30:01.3+00' },
		{ id: 'cccccccc-cccc-cccc-cccc-cccccccccccc', workflow_id: WF2_ID, workflow_name: 'Daily Report', status: 'succeeded', created_at: '2026-06-11 09:00:00+00', started_at: '2026-06-11 09:00:00.1+00', finished_at: '2026-06-11 09:00:00.9+00' }
	],
	task_statuses: { succeeded: 113, failed: 25, canceled: 1 }
};

const WORKFLOWS = [
	{
		id: WF_ID,
		name: 'Order Sync',
		slug: 'order-sync',
		is_enabled: true,
		version: 4,
		run_count: 12,
		failed_count: 2,
		created_at: '2026-06-01 08:00:00+00',
		updated_at: '2026-06-11 16:30:00+00'
	},
	{
		id: WF2_ID,
		name: 'Daily Report',
		slug: 'daily-report',
		is_enabled: false,
		version: 1,
		run_count: 5,
		failed_count: 0,
		created_at: '2026-05-20 10:00:00+00',
		updated_at: '2026-05-20 10:00:00+00'
	}
];

const WORKFLOW_DETAIL = { ...WORKFLOWS[0], definition: DEFINITION };

const RUNS = [
	{
		id: RUN_ID,
		status: 'failed',
		created_at: '2026-06-12 09:00:00+00',
		started_at: '2026-06-12 09:00:00.1+00',
		finished_at: '2026-06-12 09:00:05.4+00',
		version: 4,
		task_count: 4,
		error_summary: 'transform: ReferenceError: tite is not defined'
	},
	{
		id: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb',
		status: 'succeeded',
		created_at: '2026-06-12 08:30:00+00',
		started_at: '2026-06-12 08:30:00.1+00',
		finished_at: '2026-06-12 08:30:01.3+00',
		version: 4,
		task_count: 4,
		error_summary: null
	},
	{
		id: 'cccccccc-cccc-cccc-cccc-cccccccccccc',
		status: 'succeeded',
		created_at: '2026-06-11 09:00:00+00',
		started_at: '2026-06-11 09:00:00.1+00',
		finished_at: '2026-06-11 09:00:00.9+00',
		version: 3,
		task_count: 4,
		error_summary: null
	}
];

const RUN_DETAIL = {
	id: RUN_ID,
	workflow_id: WF_ID,
	workflow_name: 'Order Sync',
	version: 4,
	definition: DEFINITION,
	status: 'failed',
	input: null,
	error: null,
	created_at: '2026-06-12 09:00:00+00',
	started_at: '2026-06-12 09:00:00.1+00',
	finished_at: '2026-06-12 09:00:05.4+00',
	tasks: [
		{
			id: 'dddddddd-0000-0000-0000-000000000001',
			step_key: 'fetch',
			attempt: 1,
			status: 'succeeded',
			output: { status: 200, body: { title: 'June orders', orders: [1, 2, 3] } },
			error: null,
			queued_at: '2026-06-12 09:00:00.1+00',
			started_at: '2026-06-12 09:00:00.2+00',
			finished_at: '2026-06-12 09:00:00.45+00'
		},
		{
			id: 'dddddddd-0000-0000-0000-000000000002',
			step_key: 'count',
			attempt: 1,
			status: 'succeeded',
			output: 3,
			error: null,
			queued_at: '2026-06-12 09:00:00.5+00',
			started_at: '2026-06-12 09:00:00.55+00',
			finished_at: '2026-06-12 09:00:00.58+00'
		},
		{
			id: 'dddddddd-0000-0000-0000-000000000003',
			step_key: 'title',
			attempt: 1,
			status: 'failed',
			output: null,
			error: { message: 'transform: ReferenceError: tite is not defined' },
			queued_at: '2026-06-12 09:00:00.5+00',
			started_at: '2026-06-12 09:00:00.55+00',
			finished_at: '2026-06-12 09:00:00.6+00'
		},
		{
			id: 'dddddddd-0000-0000-0000-000000000004',
			step_key: 'title',
			attempt: 2,
			status: 'failed',
			output: null,
			error: { message: 'transform: ReferenceError: tite is not defined' },
			queued_at: '2026-06-12 09:00:02.6+00',
			started_at: '2026-06-12 09:00:02.65+00',
			finished_at: '2026-06-12 09:00:02.7+00'
		}
	]
};

const LOGS = [
	{ id: 8, task_id: 'dddddddd-0000-0000-0000-000000000004', step_key: 'title', ts: '2026-06-12 09:00:02.700+00', level: 8, message: 'task failed', fields: { will_retry: false, error: 'transform: ReferenceError: tite is not defined' } },
	{ id: 7, task_id: 'dddddddd-0000-0000-0000-000000000004', step_key: 'title', ts: '2026-06-12 09:00:02.650+00', level: 0, message: 'task started', fields: { type: 'transform' } },
	{ id: 6, task_id: 'dddddddd-0000-0000-0000-000000000003', step_key: 'title', ts: '2026-06-12 09:00:00.600+00', level: 4, message: 'task failed', fields: { will_retry: true, error: 'transform: ReferenceError: tite is not defined' } },
	{ id: 5, task_id: 'dddddddd-0000-0000-0000-000000000002', step_key: 'count', ts: '2026-06-12 09:00:00.580+00', level: 0, message: 'task succeeded', fields: { attempt: 1 } },
	{ id: 4, task_id: 'dddddddd-0000-0000-0000-000000000002', step_key: 'count', ts: '2026-06-12 09:00:00.550+00', level: 0, message: 'task started', fields: { type: 'transform' } },
	{ id: 3, task_id: 'dddddddd-0000-0000-0000-000000000001', step_key: 'fetch', ts: '2026-06-12 09:00:00.450+00', level: 0, message: 'task succeeded', fields: { attempt: 1 } },
	{ id: 2, task_id: 'dddddddd-0000-0000-0000-000000000001', step_key: 'fetch', ts: '2026-06-12 09:00:00.400+00', level: 0, message: 'http request done', fields: { method: 'GET', status: 200, url: 'https://api.example.com/orders' } },
	{ id: 1, task_id: 'dddddddd-0000-0000-0000-000000000001', step_key: 'fetch', ts: '2026-06-12 09:00:00.200+00', level: 0, message: 'task started', fields: { type: 'http.request' } }
];

const SECRETS = [
	{
		id: 'eeeeeeee-0000-0000-0000-000000000001',
		name: 'my_anthropic',
		type: 'api_key',
		provider: 'anthropic',
		value_hint: '…ab12',
		created_at: '2026-06-10 12:00:00+00'
	},
	{
		id: 'eeeeeeee-0000-0000-0000-000000000002',
		name: 'webhook_token',
		type: 'generic',
		value_hint: '…xy89',
		created_at: '2026-06-11 09:30:00+00'
	},
	{
		id: 'eeeeeeee-0000-0000-0000-000000000003',
		name: 'ghcr',
		type: 'registry',
		value_hint: '…tok9',
		created_at: '2026-06-11 14:00:00+00'
	}
];

const COMPUTE_TARGETS = [
	{
		id: 'cccccccc-0000-0000-0000-000000000001',
		name: 'local',
		backend: 'docker',
		cpu_limit: '1',
		memory_mb_limit: 1024,
		timeout_sec_limit: 600,
		image_allowlist: [],
		is_enabled: true,
		created_at: '2026-06-10 12:00:00+00',
		updated_at: '2026-06-10 12:00:00+00'
	},
	{
		id: 'cccccccc-0000-0000-0000-000000000002',
		name: 'cluster',
		backend: 'k8s',
		namespace: 'oarlock',
		runtime_class: 'gvisor',
		cpu_limit: '2',
		memory_mb_limit: 4096,
		timeout_sec_limit: 1800,
		image_allowlist: ['ghcr.io/acme/'],
		registry_secret_name: 'ghcr',
		is_enabled: true,
		created_at: '2026-06-10 12:00:00+00',
		updated_at: '2026-06-10 12:00:00+00'
	}
];

const MCP_SERVERS = [
	{
		id: 'ffffffff-0000-0000-0000-000000000001',
		name: 'github-tools',
		url: 'https://mcp.github.example/mcp',
		has_auth: true,
		is_enabled: true,
		created_at: '2026-06-08 10:00:00+00',
		updated_at: '2026-06-11 18:00:00+00'
	},
	{
		id: 'ffffffff-0000-0000-0000-000000000002',
		name: 'internal-search',
		url: 'http://search.internal:7777/mcp',
		has_auth: false,
		is_enabled: false,
		created_at: '2026-06-09 10:00:00+00',
		updated_at: '2026-06-09 10:00:00+00'
	}
];

const MCP_TOOLS = [
	{ name: 'search_issues', description: 'Search issues in a repository', input_schema: { type: 'object' } },
	{ name: 'create_issue', description: 'Open a new issue', input_schema: { type: 'object' } },
	{ name: 'get_file', description: 'Read a file from a repository', input_schema: { type: 'object' } }
];

const STEP_TYPES = [
	{
		type: 'http.request',
		label: 'HTTP Request',
		description: 'Call an HTTP endpoint and return the response',
		config_spec: [
			{ key: 'url', label: 'URL', kind: 'string', placeholder: 'https://api.example.com/...', required: true },
			{ key: 'method', label: 'Method', kind: 'select', options: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] },
			{ key: 'body', label: 'Body', kind: 'text', placeholder: '{"hello": "world"}' },
			{ key: 'headers', label: 'Headers (JSON)', kind: 'text', placeholder: '{"Authorization": "Bearer ..."}' }
		]
	},
	{
		type: 'transform',
		label: 'Transform (JS)',
		description: 'Run a JavaScript expression over prior step outputs',
		config_spec: [
			{ key: 'script', label: 'Script', kind: 'text', placeholder: 'return steps.fetch.body.items.length', required: true }
		]
	},
	{
		type: 'delay',
		label: 'Delay',
		description: 'Wait a fixed duration before continuing',
		config_spec: [{ key: 'seconds', label: 'Seconds', kind: 'number', placeholder: '5', required: true }]
	},
	{
		type: 'condition',
		label: 'Condition (If/Else)',
		description: 'Branch the workflow: evaluate rules (or a JS expression) and route to the Then or Else path',
		config_spec: [
			{ key: 'mode', label: 'Mode', kind: 'select', options: ['rules', 'expression'] },
			{ key: 'combinator', label: 'Match', kind: 'select', options: ['and', 'or'], visible_when: { mode: 'rules' } },
			{ key: 'rules', label: 'Rules', kind: 'rules', visible_when: { mode: 'rules' } },
			{ key: 'expression', label: 'Expression (JS)', kind: 'text', placeholder: 'steps.fetch.body.count > 0', visible_when: { mode: 'expression' } }
		]
	},
	{
		type: 'container.run',
		label: 'Run Container',
		description: 'Run any Docker image (e.g. ffprobe, ffmpeg) with files staged in and out',
		config_spec: [
			{ key: 'compute_target', label: 'Compute target', kind: 'compute_target', required: true },
			{ key: 'image', label: 'Image', kind: 'string', placeholder: 'linuxserver/ffmpeg:latest', required: true },
			{ key: 'command', label: 'Command (JSON array)', kind: 'text', placeholder: '["ffprobe","-v","quiet"]' },
			{ key: 'args', label: 'Args (JSON array)', kind: 'text', placeholder: '["-show_format","/oarlock/in/video.mp4"]' },
			{ key: 'env', label: 'Environment (JSON object)', kind: 'text', placeholder: '{"TOKEN":"{{secrets.my_token}}"}' },
			{ key: 'input_artifacts', label: 'Input artifacts (JSON)', kind: 'text', placeholder: '[{"from":"{{steps.upload.artifacts[0].id}}","as":"video.mp4"}]' },
			{ key: 'output_globs', label: 'Output files (JSON array of globs)', kind: 'text', placeholder: '["*.mp4"]' },
			{ key: 'cpu', label: 'CPU', kind: 'string', placeholder: '1' },
			{ key: 'memory_mb', label: 'Memory (MB)', kind: 'number', placeholder: '1024' },
			{ key: 'timeout_sec', label: 'Timeout (s)', kind: 'number', placeholder: '300' }
		]
	}
];

export async function mockApi(page: Page) {
	await page.route('**/v1/**', async (route) => {
		const request = route.request();
		if (request.method() === 'OPTIONS') {
			return route.fulfill({ status: 204, headers: CORS });
		}
		const path = new URL(request.url()).pathname;
		const json = (body: unknown) =>
			route.fulfill({
				status: 200,
				headers: { ...CORS, 'content-type': 'application/json' },
				body: JSON.stringify(body)
			});

		if (path === '/v1/me') {
			return json({
				user: { id: '00000000-0000-0000-0000-000000000002', email: 'dev@localhost', name: 'Dev User' },
				workspace: { id: '00000000-0000-0000-0000-000000000001', name: 'Default Workspace' },
				role: 'owner'
			});
		}
		if (path === '/v1/stats') return json(STATS);
		if (path === '/v1/workflows') return json(WORKFLOWS);
		if (path === '/v1/step-types') return json(STEP_TYPES);
		if (path === '/v1/secrets') return json(SECRETS);
		if (path === '/v1/compute-targets') return json(COMPUTE_TARGETS);
		if (path === '/v1/mcp-servers') return json(MCP_SERVERS);
		if (/^\/v1\/mcp-servers\/[^/]+\/tools$/.test(path)) return json(MCP_TOOLS);
		if (path === `/v1/workflows/${WF_ID}`) return json(WORKFLOW_DETAIL);
		if (path === `/v1/workflows/${WF_ID}/runs`) return json(RUNS);
		if (path === `/v1/workflows/${WF3_ID}`) return json(COND_WORKFLOW_DETAIL);
		if (path === `/v1/workflows/${WF3_ID}/runs`) return json(COND_RUNS);
		if (path === `/v1/runs/${RUN_ID}/logs`) return json(LOGS);
		if (path === `/v1/runs/${COND_RUN_ID}/logs`) return json([]);
		if (path === `/v1/runs/${RUN_ID}/events`) {
			return route.fulfill({
				status: 200,
				headers: { ...CORS, 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
				body: `event: run\ndata: ${JSON.stringify(RUN_DETAIL)}\n\n`
			});
		}
		if (path === `/v1/runs/${COND_RUN_ID}/events`) {
			return route.fulfill({
				status: 200,
				headers: { ...CORS, 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
				body: `event: run\ndata: ${JSON.stringify(COND_RUN_DETAIL)}\n\n`
			});
		}
		return route.fulfill({ status: 404, headers: CORS, body: '{"error":"not mocked"}' });
	});
}
