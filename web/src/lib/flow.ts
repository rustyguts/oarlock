import type { Node, Edge } from '@xyflow/svelte';
import type { Definition, Step } from './api';

export interface StepNodeData extends Record<string, unknown> {
	stepType: string;
	config: Record<string, unknown>;
	retries?: number;
	status?: string; // task status during a run
}

export type StepNode = Node<StepNodeData>;

// The JSON definition is canonical (design hard rule 4); nodes/edges are a
// projection of it. Node id == step key; edge source -> target == "target
// needs source".

export function definitionToFlow(def: Definition): { nodes: StepNode[]; edges: Edge[] } {
	const nodes: StepNode[] = (def.steps ?? []).map((s, i) => ({
		id: s.key,
		type: 'step',
		position: { x: s.ui?.x ?? 80 + i * 240, y: s.ui?.y ?? 120 },
		data: { stepType: s.type, config: s.config ?? {}, retries: s.retries }
	}));
	const edges: Edge[] = [];
	for (const s of def.steps ?? []) {
		for (const need of s.needs ?? []) {
			// A branch edge leaves the condition's named "then"/"else" handle and
			// carries the label in its id, so then+else feeding one join don't
			// collide on a `${source}->${target}` id.
			const branch = s.branches?.[need];
			edges.push({
				id: branch ? `${need}:${branch}->${s.key}` : `${need}->${s.key}`,
				source: need,
				target: s.key,
				...(branch ? { sourceHandle: branch, label: branch, data: { branch } } : {})
			});
		}
	}
	return { nodes, edges };
}

export function flowToDefinition(name: string, nodes: StepNode[], edges: Edge[]): Definition {
	const steps: Step[] = nodes.map((n) => {
		const incoming = edges.filter((e) => e.target === n.id);
		const needs = [...new Set(incoming.map((e) => e.source))];
		// Branches are rebuilt from the source handle, the source of truth for
		// which branch a successor is wired to.
		const branches: Record<string, string> = {};
		for (const e of incoming) {
			if (e.sourceHandle === 'then' || e.sourceHandle === 'else') branches[e.source] = e.sourceHandle;
		}
		return {
			key: n.id,
			type: n.data.stepType,
			...(needs.length ? { needs } : {}),
			...(Object.keys(branches).length ? { branches } : {}),
			config: n.data.config,
			...(n.data.retries ? { retries: n.data.retries } : {}),
			ui: { x: Math.round(n.position.x), y: Math.round(n.position.y) }
		};
	});
	return { name, steps };
}

// Generate a unique step key for a freshly dropped node, e.g. "http_request_2".
export function nextKey(stepType: string, nodes: StepNode[]): string {
	const base = stepType.replace(/[^a-z0-9]+/gi, '_').toLowerCase();
	let i = 1;
	while (nodes.some((n) => n.id === `${base}_${i}`)) i++;
	return `${base}_${i}`;
}

export const statusColors: Record<string, string> = {
	queued: 'border-border bg-card',
	running: 'border-blue-400 bg-blue-50 animate-pulse dark:border-blue-500 dark:bg-blue-950',
	suspended: 'border-amber-400 bg-amber-50 dark:border-amber-500 dark:bg-amber-950',
	succeeded: 'border-emerald-500 bg-emerald-50 dark:border-emerald-500 dark:bg-emerald-950',
	failed: 'border-red-500 bg-red-50 dark:border-red-500 dark:bg-red-950',
	skipped: 'border-border bg-muted',
	canceled: 'border-border bg-muted'
};

export const statusBadges: Record<string, string> = {
	queued: 'bg-muted text-muted-foreground',
	running: 'bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300',
	suspended: 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300',
	succeeded: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300',
	failed: 'bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300',
	skipped: 'bg-muted text-muted-foreground',
	canceled: 'bg-muted text-muted-foreground'
};

// Postgres ::text timestamps ("2026-06-12 00:27:14.7+00") → ISO 8601.
function pgDate(t: string): number {
	return new Date(t.replace(' ', 'T').replace(/([+-]\d\d)$/, '$1:00')).getTime();
}

export function fmtTime(t: string | null): string {
	return t ? new Date(pgDate(t)).toLocaleString() : '—';
}

export function fmtRelative(t: string | null): string {
	if (!t) return '—';
	const s = Math.max(0, Date.now() - pgDate(t)) / 1000;
	if (s < 10) return 'just now';
	if (s < 60) return `${Math.floor(s)}s ago`;
	if (s < 3600) return `${Math.floor(s / 60)}m ago`;
	if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
	return `${Math.floor(s / 86400)}d ago`;
}

export function fmtDuration(start: string | null, end: string | null): string {
	if (!start) return '—';
	const s = pgDate(start);
	const e = end ? pgDate(end) : Date.now();
	const ms = e - s;
	if (ms < 1000) return `${ms}ms`;
	if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
	return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}
