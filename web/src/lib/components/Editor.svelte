<script lang="ts">
	import { onMount } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import {
		SvelteFlow,
		Background,
		Controls,
		MiniMap,
		useSvelteFlow,
		type Edge,
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
		type Definition
	} from '$lib/api';
	import { definitionToFlow, flowToDefinition, nextKey, type StepNode } from '$lib/flow';
	import StepNodeView from './StepNode.svelte';
	import Palette from './Palette.svelte';
	import Inspector from './Inspector.svelte';
	import RunPanel from './RunPanel.svelte';
	import VersionHistory from './VersionHistory.svelte';
	import TriggersPanel from './TriggersPanel.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SaveIcon from '@lucide/svelte/icons/save';
	import HistoryIcon from '@lucide/svelte/icons/history';
	import GitCommitVerticalIcon from '@lucide/svelte/icons/git-commit-vertical';
	import ZapIcon from '@lucide/svelte/icons/zap';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import BracesIcon from '@lucide/svelte/icons/braces';

	let { workflowId }: { workflowId: string } = $props();

	const nodeTypes: NodeTypes = { step: StepNodeView };
	const { screenToFlowPosition } = useSvelteFlow();

	let nodes = $state.raw<StepNode[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let name = $state('');
	let version = $state<number | null>(null);
	// Whether triggers are allowed to fire this workflow (manual runs always work).
	// Loaded once; the workflows-list toggle is the canonical control.
	let isEnabled = $state(true);
	let stepTypes = $state<StepType[]>([]);
	let secrets = $state<Secret[]>([]);
	let mcpServers = $state<MCPServer[]>([]);
	let selectedId = $state<string | null>(null);
	let dirty = $state(false);
	let saving = $state(false);
	let notice = $state('');
	let noticeKind = $state<'ok' | 'err'>('ok');
	let activeRun = $state<Run | null>(null);
	let unwatch: (() => void) | null = null;
	let flashTimer: ReturnType<typeof setTimeout> | null = null;
	// Distinguish "no resources" from "couldn't load them" so the Inspector
	// doesn't show a misleading empty hint when the API is unreachable.
	let secretsFailed = $state(false);
	let serversFailed = $state(false);

	// Run-with-input dialog
	let inputOpen = $state(false);
	let inputText = $state('');
	let inputError = $state('');

	// Version history sheet
	let versionsOpen = $state(false);

	// Triggers sheet
	let triggersOpen = $state(false);

	// Inline workflow rename
	let editingName = $state(false);
	let nameDraft = $state('');
	let renaming = $state(false);
	let nameInput = $state<HTMLInputElement | null>(null);

	let selectedNode = $derived(nodes.find((n) => n.id === selectedId) ?? null);
	let colorMode = $derived((mode.current ?? 'light') as ColorMode);

	onMount(() => {
		(async () => {
			try {
				const [wf, types, keys, servers] = await Promise.all([
					api.getWorkflow(workflowId),
					api.stepTypes(),
					api.listSecrets().catch(() => {
						secretsFailed = true;
						return [] as Secret[];
					}),
					api.listMCPServers().catch(() => {
						serversFailed = true;
						return [] as MCPServer[];
					})
				]);
				name = wf.name;
				version = wf.version;
				isEnabled = wf.is_enabled;
				stepTypes = types;
				secrets = keys;
				mcpServers = servers;
				const flow = definitionToFlow(wf.definition ?? { steps: [] });
				nodes = flow.nodes;
				edges = flow.edges;
			} catch (e) {
				flash(e instanceof Error ? e.message : String(e), 'err');
			}
		})();
		return () => unwatch?.();
	});

	// Guard client-side navigation (incl. the Back button) while there are
	// unsaved canvas changes. Full-page unloads (reload / close / external link)
	// are handled by the beforeunload listener below, not a confirm() the
	// browser would block mid-unload.
	beforeNavigate((nav) => {
		if (nav.willUnload) return;
		if (dirty && !confirm('You have unsaved changes. Leave without saving?')) {
			nav.cancel();
		}
	});

	// Guard a full page unload / reload while dirty.
	$effect(() => {
		if (!dirty) return;
		const handler = (e: BeforeUnloadEvent) => {
			e.preventDefault();
			e.returnValue = '';
		};
		window.addEventListener('beforeunload', handler);
		return () => window.removeEventListener('beforeunload', handler);
	});

	function flash(msg: string, kind: 'ok' | 'err' = 'ok') {
		notice = msg;
		noticeKind = kind;
		if (flashTimer) clearTimeout(flashTimer);
		flashTimer = setTimeout(() => (notice = ''), kind === 'err' ? 6000 : 2500);
	}

	function markDirty() {
		dirty = true;
	}

	// --- inline rename (PATCH; independent of canvas versions) ---

	function startRename() {
		nameDraft = name;
		editingName = true;
	}

	// Focus + select the field once it mounts.
	$effect(() => {
		if (editingName && nameInput) {
			nameInput.focus();
			nameInput.select();
		}
	});

	async function commitRename() {
		if (!editingName) return; // guard double-fire (Enter blurs, then onblur)
		const next = nameDraft.trim();
		editingName = false;
		if (!next) {
			flash('Name cannot be empty', 'err');
			return;
		}
		if (next === name) return;
		renaming = true;
		try {
			const res = await api.patchWorkflow(workflowId, { name: next });
			name = res.name;
			flash('Renamed');
		} catch (e) {
			flash(e instanceof Error ? e.message : String(e), 'err');
		} finally {
			renaming = false;
		}
	}

	function cancelRename() {
		editingName = false;
	}

	// --- restore a prior version (writes it as a new version, swaps the canvas) ---

	async function restoreVersion(definition: Definition, sourceVersion: number) {
		// Keep the current workflow title — restore brings back the graph, not the name.
		const def: Definition = { ...definition, name };
		const res = await api.saveDefinition(workflowId, def);
		const flow = definitionToFlow(def);
		nodes = flow.nodes;
		edges = flow.edges;
		version = res.version;
		dirty = false;
		selectedId = null;
		unwatch?.();
		activeRun = null;
		flash(`Restored v${sourceVersion} as v${res.version}`);
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
		addStep(stepType, screenToFlowPosition({ x: e.clientX, y: e.clientY }));
	}

	// A non-overlapping canvas slot for a click-added step.
	function freePosition(): { x: number; y: number } {
		let pos = { x: 120, y: 120 };
		const taken = (p: { x: number; y: number }) =>
			nodes.some((n) => Math.abs(n.position.x - p.x) < 30 && Math.abs(n.position.y - p.y) < 30);
		while (taken(pos)) pos = { x: pos.x + 48, y: pos.y + 48 };
		return pos;
	}

	function addStep(stepType: string, position = freePosition()) {
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
		}
		return config;
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

	function ontimeout(id: string, timeout: number | undefined) {
		nodes = nodes.map((n) => (n.id === id ? { ...n, data: { ...n.data, timeout } } : n));
		markDirty();
	}

	function onif(id: string, condition: string | undefined) {
		nodes = nodes.map((n) => (n.id === id ? { ...n, data: { ...n.data, if: condition } } : n));
		markDirty();
	}

	// Rewrite `steps.<oldKey>` references in a config's string values, avoiding
	// partial matches (steps.fetch must not touch steps.fetch2 or steps.fetch-b).
	function rewriteConfigRefs(
		config: Record<string, unknown>,
		oldKey: string,
		newKey: string
	): Record<string, unknown> {
		const esc = oldKey.replace(/[.*+?^${}()|[\]\\-]/g, '\\$&');
		const re = new RegExp(`(?<![\\w$])steps\\.${esc}(?![\\w-])`, 'g');
		const out: Record<string, unknown> = {};
		for (const [k, v] of Object.entries(config)) {
			out[k] = typeof v === 'string' ? v.replace(re, `steps.${newKey}`) : v;
		}
		return out;
	}

	function onrename(oldId: string, newId: string): boolean {
		if (nodes.some((n) => n.id === newId)) {
			flash(`Step key "${newId}" already exists`, 'err');
			return false;
		}
		nodes = nodes.map((n) => ({
			...n,
			id: n.id === oldId ? newId : n.id,
			data: { ...n.data, config: rewriteConfigRefs(n.data.config, oldId, newId) }
		}));
		edges = edges.map((e) => ({
			...e,
			id: `${e.source === oldId ? newId : e.source}->${e.target === oldId ? newId : e.target}`,
			source: e.source === oldId ? newId : e.source,
			target: e.target === oldId ? newId : e.target
		}));
		selectedId = newId;
		markDirty();
		return true;
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

	async function run(input: Record<string, unknown> = {}) {
		if (nodes.length === 0) {
			flash('Add at least one step before running', 'err');
			return;
		}
		if (dirty && !(await save())) return;
		try {
			const { run_id } = await api.startRun(workflowId, input);
			watch(run_id);
		} catch (e) {
			flash(e instanceof Error ? e.message : String(e), 'err');
		}
	}

	let inputStorageKey = $derived(`oarlock:input:${workflowId}`);

	function openInput() {
		inputError = '';
		try {
			inputText = localStorage.getItem(inputStorageKey) ?? '';
		} catch {
			inputText = '';
		}
		inputOpen = true;
	}

	function submitInput(e: SubmitEvent) {
		e.preventDefault();
		const text = inputText.trim();
		let parsed: Record<string, unknown> = {};
		if (text) {
			try {
				parsed = JSON.parse(text);
			} catch {
				inputError = 'Not valid JSON.';
				return;
			}
			if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
				inputError = 'Input must be a JSON object.';
				return;
			}
		}
		try {
			localStorage.setItem(inputStorageKey, text);
		} catch {
			/* localStorage unavailable — ignore */
		}
		inputOpen = false;
		run(parsed);
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
		{#if editingName}
			<Input
				bind:value={nameDraft}
				bind:ref={nameInput}
				onblur={commitRename}
				onkeydown={(e: KeyboardEvent) => {
					if (e.key === 'Enter') commitRename();
					else if (e.key === 'Escape') cancelRename();
				}}
				aria-label="Workflow name"
				class="h-8 w-56 font-medium"
			/>
		{:else}
			<button
				type="button"
				onclick={startRename}
				disabled={renaming}
				class="group hover:bg-muted/60 -mx-1 flex items-center gap-1.5 rounded-md px-1 py-0.5"
				title="Rename workflow"
			>
				<span class="font-medium">{name || '…'}</span>
				<PencilIcon
					class="text-muted-foreground size-3.5 opacity-0 transition-opacity group-hover:opacity-100"
				/>
			</button>
		{/if}
		<Badge variant="secondary">v{version ?? '—'}{dirty ? ' · unsaved' : ''}</Badge>
		{#if notice}
			<span class="text-xs {noticeKind === 'err' ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'}">
				{notice}
			</span>
		{/if}
		<div class="ml-auto flex gap-2">
			<Button
				variant="ghost"
				size="icon"
				onclick={() => (triggersOpen = true)}
				aria-label="Triggers"
				title="Triggers"
			>
				<ZapIcon class="size-4" />
			</Button>
			<Button
				variant="ghost"
				size="icon"
				onclick={() => (versionsOpen = true)}
				aria-label="Version history"
				title="Version history"
			>
				<GitCommitVerticalIcon class="size-4" />
			</Button>
			<Button variant="ghost" size="icon" href="/workflows/{workflowId}/runs" aria-label="Run history">
				<HistoryIcon class="size-4" />
			</Button>
			<Button variant="outline" onclick={save} disabled={saving || !dirty}>
				<SaveIcon class="size-4" />
				{saving ? 'Saving…' : 'Save'}
			</Button>
			<Button variant="outline" size="icon" onclick={openInput} aria-label="Run with input…" title="Run with input…">
				<BracesIcon class="size-4" />
			</Button>
			<Button onclick={() => run()}>
				<PlayIcon class="size-4" /> Run
			</Button>
		</div>
	</div>

	<div class="relative flex min-h-0 flex-1">
		<Palette {stepTypes} onadd={addStep} />
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
				secretsError={secretsFailed}
				serversError={serversFailed}
				{onconfig}
				{onretries}
				{ontimeout}
				{onif}
				{onrename}
				{ondelete}
			/>
		{/if}
	</div>
</div>

<VersionHistory bind:open={versionsOpen} {workflowId} {dirty} onrestore={restoreVersion} />

<TriggersPanel bind:open={triggersOpen} {workflowId} workflowEnabled={isEnabled} />

<Dialog.Root bind:open={inputOpen}>
	<Dialog.Content class="sm:max-w-md">
		<form onsubmit={submitInput}>
			<Dialog.Header>
				<Dialog.Title>Run with input</Dialog.Title>
				<Dialog.Description>
					A JSON object bound as <code class="bg-muted rounded px-1">{'{{ input }}'}</code> for this run.
				</Dialog.Description>
			</Dialog.Header>
			<div class="mt-4 space-y-2">
				<Textarea
					bind:value={inputText}
					rows={7}
					spellcheck={false}
					placeholder={'{\n  "order_id": 42\n}'}
					class="font-mono text-xs"
				/>
				{#if inputError}
					<p class="text-destructive text-xs">{inputError}</p>
				{/if}
			</div>
			<Dialog.Footer class="mt-4">
				<Button type="button" variant="outline" onclick={() => (inputOpen = false)}>Cancel</Button>
				<Button type="submit">
					<PlayIcon class="size-4" /> Run
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
