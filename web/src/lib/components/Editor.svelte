<script lang="ts">
	import { onMount } from 'svelte';
	import {
		SvelteFlow,
		Background,
		Controls,
		MiniMap,
		useSvelteFlow,
		type Edge,
		type Connection,
		type NodeTypes,
		type ColorMode
	} from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { mode } from 'mode-watcher';

	import {
		api,
		watchRun,
		type StepType,
		type Run,
		type Secret,
		type MCPServer,
		type ComputeTarget
	} from '$lib/api';
	import { definitionToFlow, flowToDefinition, nextKey, type StepNode } from '$lib/flow';
	import StepNodeView from './StepNode.svelte';
	import Palette from './Palette.svelte';
	import Inspector from './Inspector.svelte';
	import RunPanel from './RunPanel.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SaveIcon from '@lucide/svelte/icons/save';
	import HistoryIcon from '@lucide/svelte/icons/history';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';

	let { workflowId }: { workflowId: string } = $props();

	const nodeTypes: NodeTypes = { step: StepNodeView };
	const { screenToFlowPosition } = useSvelteFlow();

	let nodes = $state.raw<StepNode[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let name = $state('');
	let version = $state<number | null>(null);
	let stepTypes = $state<StepType[]>([]);
	let secrets = $state<Secret[]>([]);
	let mcpServers = $state<MCPServer[]>([]);
	let computeTargets = $state<ComputeTarget[]>([]);
	let selectedId = $state<string | null>(null);
	let dirty = $state(false);
	let saving = $state(false);
	let notice = $state('');
	let noticeKind = $state<'ok' | 'err'>('ok');
	let activeRun = $state<Run | null>(null);
	let unwatch: (() => void) | null = null;

	let selectedNode = $derived(nodes.find((n) => n.id === selectedId) ?? null);
	let colorMode = $derived((mode.current ?? 'light') as ColorMode);

	onMount(() => {
		(async () => {
			try {
				const [wf, types, keys, servers, targets] = await Promise.all([
					api.getWorkflow(workflowId),
					api.stepTypes(),
					api.listSecrets().catch(() => []),
					api.listMCPServers().catch(() => []),
					api.listComputeTargets().catch(() => [])
				]);
				name = wf.name;
				version = wf.version;
				stepTypes = types;
				secrets = keys;
				mcpServers = servers;
				computeTargets = targets;
				const flow = definitionToFlow(wf.definition ?? { steps: [] });
				nodes = flow.nodes;
				edges = flow.edges;
			} catch (e) {
				flash(e instanceof Error ? e.message : String(e), 'err');
			}
		})();
		return () => unwatch?.();
	});

	function flash(msg: string, kind: 'ok' | 'err' = 'ok') {
		notice = msg;
		noticeKind = kind;
		setTimeout(() => (notice = ''), kind === 'err' ? 6000 : 2500);
	}

	function markDirty() {
		dirty = true;
	}

	// --- drag & drop from palette ---

	function ondragover(e: DragEvent) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
	}

	function ondrop(e: DragEvent) {
		e.preventDefault();
		const stepType = e.dataTransfer?.getData('application/oarlock-step');
		if (!stepType) return;
		const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
		const key = nextKey(stepType, nodes);
		nodes = [
			...nodes,
			{ id: key, type: 'step', position, data: { stepType, config: defaultConfig(stepType) } }
		];
		selectedId = key;
		markDirty();
	}

	function defaultConfig(stepType: string): Record<string, unknown> {
		const spec = stepTypes.find((t) => t.type === stepType);
		const config: Record<string, unknown> = {};
		for (const f of spec?.config_spec ?? []) {
			if (f.kind === 'select' && f.options?.length) config[f.key] = f.options[0];
			if (f.kind === 'rules') config[f.key] = [];
		}
		return config;
	}

	// A connection from a condition's "then"/"else" handle becomes a branch edge:
	// the label is stamped into the id (so then+else into one join don't collide)
	// and carried on the edge. onbeforeconnect returns the edge to add — onconnect
	// only fires afterward, so it can't set the id.
	function onbeforeconnect(connection: Connection): Edge {
		const { source, target, sourceHandle } = connection;
		const branch = sourceHandle === 'then' || sourceHandle === 'else' ? sourceHandle : undefined;
		return {
			...connection,
			id: branch ? `${source}:${branch}->${target}` : `${source}->${target}`,
			...(branch ? { sourceHandle: branch, label: branch, data: { branch } } : {})
		};
	}

	// --- inspector callbacks ---

	function onconfig(id: string, config: Record<string, unknown>) {
		nodes = nodes.map((n) => (n.id === id ? { ...n, data: { ...n.data, config } } : n));
		markDirty();
	}

	function onretries(id: string, retries: number) {
		nodes = nodes.map((n) => (n.id === id ? { ...n, data: { ...n.data, retries } } : n));
		markDirty();
	}

	function onrename(oldId: string, newId: string) {
		if (nodes.some((n) => n.id === newId)) {
			flash(`Step key "${newId}" already exists`, 'err');
			return;
		}
		nodes = nodes.map((n) => (n.id === oldId ? { ...n, id: newId } : n));
		edges = edges.map((e) => {
			const source = e.source === oldId ? newId : e.source;
			const target = e.target === oldId ? newId : e.target;
			// Preserve the branch segment so branch edge ids stay unique and
			// flowToDefinition still derives branches from the source handle.
			const seg = e.sourceHandle === 'then' || e.sourceHandle === 'else' ? `:${e.sourceHandle}` : '';
			return { ...e, id: `${source}${seg}->${target}`, source, target };
		});
		selectedId = newId;
		markDirty();
	}

	function ondelete(id: string) {
		nodes = nodes.filter((n) => n.id !== id);
		edges = edges.filter((e) => e.source !== id && e.target !== id);
		if (selectedId === id) selectedId = null;
		markDirty();
	}

	// --- save & run (live updates over SSE) ---

	async function save(): Promise<boolean> {
		saving = true;
		try {
			const def = flowToDefinition(name, nodes, edges);
			const res = await api.saveDefinition(workflowId, def);
			version = res.version;
			dirty = false;
			flash(`Saved v${res.version}`);
			return true;
		} catch (e) {
			flash(e instanceof Error ? e.message : String(e), 'err');
			return false;
		} finally {
			saving = false;
		}
	}

	function applyRun(run: Run) {
		activeRun = run;
		const byStep: Record<string, string> = {};
		for (const t of run.tasks) byStep[t.step_key] = t.status;
		nodes = nodes.map((n) => ({ ...n, data: { ...n.data, status: byStep[n.id] } }));
	}

	function watch(runId: string) {
		unwatch?.();
		unwatch = watchRun(runId, applyRun, (err) => flash(err.message, 'err'));
	}

	async function run() {
		if (nodes.length === 0) {
			flash('Add at least one step before running', 'err');
			return;
		}
		if (dirty && !(await save())) return;
		try {
			const { run_id } = await api.startRun(workflowId);
			watch(run_id);
		} catch (e) {
			flash(e instanceof Error ? e.message : String(e), 'err');
		}
	}

	async function cancelRun() {
		if (!activeRun) return;
		try {
			await api.cancelRun(activeRun.id);
		} catch (e) {
			flash(e instanceof Error ? e.message : String(e), 'err');
		}
	}

	async function retryRun() {
		if (!activeRun) return;
		try {
			await api.retryRun(activeRun.id);
			watch(activeRun.id);
		} catch (e) {
			flash(e instanceof Error ? e.message : String(e), 'err');
		}
	}

	function closeRun() {
		unwatch?.();
		activeRun = null;
		nodes = nodes.map((n) => ({ ...n, data: { ...n.data, status: undefined } }));
	}
</script>

<div class="flex h-full min-h-0 flex-col">
	<div class="bg-background flex h-12 shrink-0 items-center gap-3 border-b px-4">
		<Button variant="ghost" size="icon" href="/workflows" aria-label="Back">
			<ArrowLeftIcon class="size-4" />
		</Button>
		<span class="font-medium">{name || '…'}</span>
		<Badge variant="secondary">v{version ?? '—'}{dirty ? ' · unsaved' : ''}</Badge>
		{#if notice}
			<span class="text-xs {noticeKind === 'err' ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'}">
				{notice}
			</span>
		{/if}
		<div class="ml-auto flex gap-2">
			<Button variant="ghost" size="icon" href="/workflows/{workflowId}/runs" aria-label="Run history">
				<HistoryIcon class="size-4" />
			</Button>
			<Button variant="outline" onclick={save} disabled={saving || !dirty}>
				<SaveIcon class="size-4" />
				{saving ? 'Saving…' : 'Save'}
			</Button>
			<Button onclick={run}>
				<PlayIcon class="size-4" /> Run
			</Button>
		</div>
	</div>

	<div class="relative flex min-h-0 flex-1">
		<Palette {stepTypes} />
		<div class="relative min-w-0 flex-1">
			<SvelteFlow
				bind:nodes
				bind:edges
				{nodeTypes}
				{colorMode}
				fitView
				{ondrop}
				{ondragover}
				onnodeclick={({ node }) => (selectedId = node.id)}
				onpaneclick={() => (selectedId = null)}
				onnodedragstop={markDirty}
				{onbeforeconnect}
				onconnect={markDirty}
				ondelete={markDirty}
				proOptions={{ hideAttribution: true }}
			>
				<Background />
				<Controls />
				<MiniMap />
			</SvelteFlow>
			{#if activeRun}
				<RunPanel run={activeRun} onclose={closeRun} oncancel={cancelRun} onretry={retryRun} />
			{/if}
		</div>
		{#if selectedNode}
			<Inspector
				node={selectedNode}
				{stepTypes}
				{secrets}
				{mcpServers}
				{computeTargets}
				{onconfig}
				{onretries}
				{onrename}
				{ondelete}
			/>
		{/if}
	</div>
</div>
