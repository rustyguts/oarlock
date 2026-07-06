import type { Page } from '@playwright/test';

// Deterministic API fixtures served via request interception — snapshot tests
// never touch the real backend. Frozen "now" in the specs is 09:05:00Z.

export const WF_ID = '11111111-1111-1111-1111-111111111111';
export const WF2_ID = '22222222-2222-2222-2222-222222222222';
export const RUN_ID = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';

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

// A workflow whose canvas exercises the dynamic-select config kinds
// (api_key / mcp_server / mcp_tool) so the Inspector's dynamic branch renders.
const AI_DEFINITION = {
	name: 'Daily Report',
	steps: [
		{
			key: 'fetch',
			type: 'http.request',
			config: { url: 'https://api.example.com/orders', method: 'GET' },
			ui: { x: 80, y: 160 }
		},
		{
			key: 'summarize',
			type: 'ai.prompt',
			needs: ['fetch'],
			config: {
				api_key: 'my_anthropic',
				model: 'claude-3-5-sonnet',
				prompt: 'Summarize the orders in {{ steps.fetch.body }} for the daily report.',
				server: 'github-tools',
				tool: 'search_issues'
			},
			ui: { x: 380, y: 160 }
		}
	]
};

// Immutable version history for WF_ID (newest first).
const VERSIONS = [
	{ version: 4, created_at: '2026-06-11 16:30:00+00', step_count: 4, is_current: true },
	{ version: 3, created_at: '2026-06-10 14:00:00+00', step_count: 4, is_current: false },
	{ version: 2, created_at: '2026-06-05 11:00:00+00', step_count: 3, is_current: false },
	{ version: 1, created_at: '2026-06-01 08:00:00+00', step_count: 2, is_current: false }
];

// Triggers for WF_ID: one schedule (enabled) + one signed webhook (disabled).
const TRIGGERS = [
	{
		id: '99999999-0000-0000-0000-000000000001',
		type: 'schedule',
		config: { cron: '*/5 * * * *' },
		is_enabled: true,
		created_at: '2026-06-10 12:00:00+00'
	},
	{
		id: '99999999-0000-0000-0000-000000000002',
		type: 'webhook',
		config: { path: 'orders-hook', secret: 's3cr3t' },
		is_enabled: false,
		created_at: '2026-06-11 09:30:00+00'
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
const WF2_DETAIL = { ...WORKFLOWS[1], definition: AI_DEFINITION };

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

// A run exercising the `skipped` status: `notify` has a falsy `if`, so the
// engine skips it. Drives the skipped node border + badge styling.
export const SKIP_RUN_ID = 'a5a5a5a5-a5a5-a5a5-a5a5-a5a5a5a5a5a5';

const SKIP_DEFINITION = {
	name: 'order-sync',
	steps: [
		{
			key: 'fetch',
			type: 'http.request',
			config: { url: 'https://api.example.com/orders', method: 'GET' },
			ui: { x: 80, y: 160 }
		},
		{
			key: 'filter',
			type: 'code.js',
			needs: ['fetch'],
			config: { script: 'return steps.fetch.body.orders.filter(o => o > 1)' },
			ui: { x: 360, y: 160 }
		},
		{
			key: 'notify',
			type: 'code.js',
			needs: ['filter'],
			if: 'steps.filter.length > 5',
			config: { script: 'console.log("notifying")' },
			ui: { x: 640, y: 160 }
		}
	]
};

const SKIP_RUN_DETAIL = {
	id: SKIP_RUN_ID,
	workflow_id: WF_ID,
	workflow_name: 'Order Sync',
	version: 4,
	definition: SKIP_DEFINITION,
	status: 'succeeded',
	input: null,
	error: null,
	created_at: '2026-06-12 08:45:00+00',
	started_at: '2026-06-12 08:45:00.1+00',
	finished_at: '2026-06-12 08:45:00.9+00',
	tasks: [
		{
			id: 'a5a5a5a5-0000-0000-0000-000000000001',
			step_key: 'fetch',
			attempt: 1,
			status: 'succeeded',
			output: { status: 200, body: { orders: [1, 2, 3] } },
			error: null,
			queued_at: '2026-06-12 08:45:00.1+00',
			started_at: '2026-06-12 08:45:00.2+00',
			finished_at: '2026-06-12 08:45:00.4+00'
		},
		{
			id: 'a5a5a5a5-0000-0000-0000-000000000002',
			step_key: 'filter',
			attempt: 1,
			status: 'succeeded',
			output: [2, 3],
			error: null,
			queued_at: '2026-06-12 08:45:00.4+00',
			started_at: '2026-06-12 08:45:00.5+00',
			finished_at: '2026-06-12 08:45:00.6+00'
		},
		{
			id: 'a5a5a5a5-0000-0000-0000-000000000003',
			step_key: 'notify',
			attempt: 1,
			status: 'skipped',
			output: null,
			error: null,
			queued_at: '2026-06-12 08:45:00.6+00',
			started_at: null,
			finished_at: '2026-06-12 08:45:00.7+00'
		}
	]
};

// A run parked on a `wait.callback` step: the run stays `running` while the
// `approve` task is `suspended` with a resume_url in its output. Drives the
// amber suspended node + the "Waiting for callback" card.
export const SUSPEND_RUN_ID = 'c0ffee00-0000-0000-0000-c0ffee000000';

const SUSPEND_DEFINITION = {
	name: 'order-sync',
	steps: [
		{
			key: 'fetch',
			type: 'http.request',
			config: { url: 'https://api.example.com/orders', method: 'GET' },
			ui: { x: 80, y: 160 }
		},
		{
			key: 'approve',
			type: 'wait.callback',
			needs: ['fetch'],
			config: { note: 'Approve the payout before continuing' },
			ui: { x: 360, y: 160 }
		},
		{
			key: 'notify',
			type: 'code.js',
			needs: ['approve'],
			config: { script: 'console.log("approved")' },
			ui: { x: 640, y: 160 }
		}
	]
};

const SUSPEND_RUN_DETAIL = {
	id: SUSPEND_RUN_ID,
	workflow_id: WF_ID,
	workflow_name: 'Order Sync',
	version: 4,
	definition: SUSPEND_DEFINITION,
	status: 'running',
	input: null,
	error: null,
	created_at: '2026-06-12 09:00:00+00',
	started_at: '2026-06-12 09:00:00.1+00',
	finished_at: null,
	tasks: [
		{
			id: 'c0ffee00-0000-0000-0000-000000000001',
			step_key: 'fetch',
			attempt: 1,
			status: 'succeeded',
			output: { status: 200, body: { orders: [1, 2, 3] } },
			error: null,
			queued_at: '2026-06-12 09:00:00.1+00',
			started_at: '2026-06-12 09:00:00.2+00',
			finished_at: '2026-06-12 09:00:00.4+00'
		},
		{
			id: 'c0ffee00-0000-0000-0000-000000000002',
			step_key: 'approve',
			attempt: 1,
			status: 'suspended',
			output: {
				resume_url: '/resume/rsm_7f3a9c1e4b2d',
				waiting: true,
				note: 'Approve the payout before continuing'
			},
			error: null,
			queued_at: '2026-06-12 09:00:00.5+00',
			started_at: '2026-06-12 09:00:00.55+00',
			finished_at: null
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
	}
];

const USERS = [
	{
		id: '00000000-0000-0000-0000-000000000002',
		email: 'dev@localhost',
		name: 'Dev User',
		role: 'owner',
		must_change_password: false,
		created_at: '2026-05-20 10:00:00+00',
		last_seen_at: '2026-06-12 09:00:00+00'
	},
	{
		id: '00000000-0000-0000-0000-000000000003',
		email: 'grace@example.com',
		name: 'Grace Hopper',
		role: 'editor',
		must_change_password: true,
		created_at: '2026-06-10 12:00:00+00',
		last_seen_at: null
	}
];

const API_TOKENS = [
	{
		id: 'a70e0000-0000-0000-0000-000000000001',
		name: 'claude-desktop',
		prefix: 'oak_5c84',
		created_at: '2026-06-10 12:00:00+00',
		last_used_at: '2026-06-12 08:55:00+00'
	},
	{
		id: 'a70e0000-0000-0000-0000-000000000002',
		name: 'ci-runner',
		prefix: 'oak_9a2f',
		created_at: '2026-06-01 08:00:00+00',
		last_used_at: null
	}
];

// Deterministic raw token surfaced once by the create-token dialog.
const NEW_TOKEN = 'oak_1234567890abcdef1234567890abcdef1234567890abcdef';

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
		type: 'code.js',
		label: 'Code (JS)',
		description: 'Run JavaScript with console.log captured to the task log',
		config_spec: [
			{
				key: 'script',
				label: 'Script',
				kind: 'text',
				placeholder: 'const items = steps.fetch.body.items\nreturn items.filter(i => i.active)',
				required: true
			}
		]
	},
	{
		type: 'ai.prompt',
		label: 'AI Prompt',
		description: 'Send a prompt to an LLM, optionally with MCP tools',
		config_spec: [
			{ key: 'api_key', label: 'API Key', kind: 'api_key', required: true },
			{ key: 'model', label: 'Model', kind: 'select', options: ['claude-3-5-sonnet', 'gpt-4o'] },
			{ key: 'prompt', label: 'Prompt', kind: 'text', placeholder: 'Summarize {{ steps.fetch.body }}', required: true },
			{ key: 'server', label: 'MCP Server', kind: 'mcp_server' },
			{ key: 'tool', label: 'Tool', kind: 'mcp_tool' }
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
		const method = request.method();
		const json = (body: unknown) =>
			route.fulfill({
				status: 200,
				headers: { ...CORS, 'content-type': 'application/json' },
				body: JSON.stringify(body)
			});

		// --- mutations (branch on method before the GET routes) ---
		if (method === 'PATCH' && /^\/v1\/workflows\/[^/]+$/.test(path)) {
			const id = path.split('/').pop()!;
			const patch = JSON.parse(request.postData() || '{}');
			const wf = [WORKFLOW_DETAIL, WF2_DETAIL].find((w) => w.id === id) ?? WORKFLOW_DETAIL;
			return json({
				id,
				name: patch.name ?? wf.name,
				is_enabled: patch.is_enabled ?? wf.is_enabled
			});
		}
		if (method === 'PUT' && /^\/v1\/secrets\/[^/]+$/.test(path)) {
			return json({ id: path.split('/').pop() });
		}
		// Trigger mutations: create / patch / delete.
		if (method === 'POST' && /^\/v1\/workflows\/[^/]+\/triggers$/.test(path)) {
			return route.fulfill({
				status: 201,
				headers: { ...CORS, 'content-type': 'application/json' },
				body: JSON.stringify({ id: '99999999-0000-0000-0000-00000000000f' })
			});
		}
		if (method === 'PATCH' && /^\/v1\/triggers\/[^/]+$/.test(path)) {
			return json({ id: path.split('/').pop() });
		}
		if (method === 'DELETE' && /^\/v1\/triggers\/[^/]+$/.test(path)) {
			return route.fulfill({ status: 204, headers: CORS });
		}
		// API tokens: create returns the raw token once; delete is a no-op 204.
		if (method === 'POST' && path === '/v1/api-tokens') {
			return route.fulfill({
				status: 201,
				headers: { ...CORS, 'content-type': 'application/json' },
				body: JSON.stringify({ id: 'a70e0000-0000-0000-0000-0000000000ff', token: NEW_TOKEN })
			});
		}
		if (method === 'DELETE' && /^\/v1\/api-tokens\/[^/]+$/.test(path)) {
			return route.fulfill({ status: 204, headers: CORS });
		}

		if (path === '/v1/me') {
			return json({
				auth_kind: 'session',
				user: { id: '00000000-0000-0000-0000-000000000002', email: 'dev@localhost', name: 'Dev User' },
				workspace: { id: '00000000-0000-0000-0000-000000000001', name: 'Default Workspace', slug: 'default' },
				role: 'owner',
				must_change_password: false,
				vault: { dev_key: false }
			});
		}
		if (path === '/v1/stats') return json(STATS);
		if (path === '/v1/users') return json(USERS);
		if (path === '/v1/api-tokens') return json(API_TOKENS);
		if (path === '/v1/workflows') return json(WORKFLOWS);
		if (path === '/v1/step-types') return json(STEP_TYPES);
		if (path === '/v1/secrets') return json(SECRETS);
		if (path === '/v1/mcp-servers/test') return json(MCP_TOOLS);
		if (path === '/v1/mcp-servers') return json(MCP_SERVERS);
		if (/^\/v1\/mcp-servers\/[^/]+\/tools$/.test(path)) return json(MCP_TOOLS);
		if (path === `/v1/workflows/${WF_ID}/triggers`) return json(TRIGGERS);
		if (/^\/v1\/workflows\/[^/]+\/triggers$/.test(path)) return json([]);
		if (path === `/v1/workflows/${WF_ID}/versions`) return json(VERSIONS);
		if (/^\/v1\/workflows\/[^/]+\/versions\/\d+$/.test(path)) {
			const version = Number(path.split('/').pop());
			return json({ version, created_at: '2026-06-11 16:30:00+00', definition: DEFINITION });
		}
		if (path === `/v1/workflows/${WF_ID}`) return json(WORKFLOW_DETAIL);
		if (path === `/v1/workflows/${WF2_ID}`) return json(WF2_DETAIL);
		if (path === `/v1/workflows/${WF_ID}/runs`) return json(RUNS);
		if (path === `/v1/runs/${RUN_ID}`) return json(RUN_DETAIL);
		if (path === `/v1/runs/${SKIP_RUN_ID}`) return json(SKIP_RUN_DETAIL);
		if (path === `/v1/runs/${SUSPEND_RUN_ID}`) return json(SUSPEND_RUN_DETAIL);
		if (path === `/v1/runs/${RUN_ID}/logs`) return json(LOGS);
		if (path === `/v1/runs/${RUN_ID}/events`) {
			return route.fulfill({
				status: 200,
				headers: { ...CORS, 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
				body: `event: run\ndata: ${JSON.stringify(RUN_DETAIL)}\n\n`
			});
		}
		if (path === `/v1/runs/${SUSPEND_RUN_ID}/events`) {
			// The run is still running (a suspended task keeps it alive); a long
			// retry keeps EventSource from reconnecting and churning networkidle.
			return route.fulfill({
				status: 200,
				headers: { ...CORS, 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
				body: `retry: 3600000\nevent: run\ndata: ${JSON.stringify(SUSPEND_RUN_DETAIL)}\n\n`
			});
		}
		return route.fulfill({ status: 404, headers: CORS, body: '{"error":"not mocked"}' });
	});
}
