<script lang="ts">
	import { onMount } from 'svelte';
	import {
		SvelteFlow,
		SvelteFlowProvider,
		Background,
		Controls,
		MiniMap,
		type Edge,
		type NodeTypes,
		type ColorMode
	} from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { mode } from 'mode-watcher';

	import { api, ApiError, API_BASE, watchRun, type Run, type TaskInfo } from '$lib/api';
	import { definitionToFlow, statusBadges, fmtDuration, fmtTime, fmtRelative, type StepNode } from '$lib/flow';
	import StepNodeView from '$lib/components/StepNode.svelte';
	import RunLogs from '$lib/components/RunLogs.svelte';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import BanIcon from '@lucide/svelte/icons/ban';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import XIcon from '@lucide/svelte/icons/x';
	import SearchXIcon from '@lucide/svelte/icons/search-x';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import CheckIcon from '@lucide/svelte/icons/check';

	let { runId }: { runId: string } = $props();
	const nodeTypes: NodeTypes = { step: StepNodeView };

	let run = $state<Run | null>(null);
	let nodes = $state.raw<StepNode[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let selectedStep = $state<string | null>(null);
	let tab = $state<'tasks' | 'logs'>('tasks');
	let error = $state('');
	let notFound = $state(false);
	let loaded = $state(false);
	let unwatch: (() => void) | null = null;

	let active = $derived(run ? ['queued', 'running', 'suspended'].includes(run.status) : false);
	let retryable = $derived(run ? ['failed', 'canceled'].includes(run.status) : false);
	let colorMode = $derived((mode.current ?? 'light') as ColorMode);

	// A run's input is worth showing only when it carries data.
	let hasInput = $derived(
		!!run &&
			run.input != null &&
			!(typeof run.input === 'object' && Object.keys(run.input as object).length === 0)
	);

	// All attempts for the selected step, newest first.
	let selectedAttempts = $derived(
		run && selectedStep
			? run.tasks.filter((t) => t.step_key === selectedStep).sort((a, b) => b.attempt - a.attempt)
			: []
	);

	function applyRun(r: Run) {
		const first = run === null;
		run = r;
		const byStep: Record<string, string> = {};
		for (const t of r.tasks) byStep[t.step_key] = t.status; // tasks ordered by attempt: last wins
		if (first) {
			// Build the canvas once from the pinned definition the run executed.
			const flow = definitionToFlow(r.definition ?? { steps: [] });
			nodes = flow.nodes.map((n) => ({ ...n, data: { ...n.data, status: byStep[n.id] } }));
			edges = flow.edges;
		} else {
			nodes = nodes.map((n) => ({ ...n, data: { ...n.data, status: byStep[n.id] } }));
		}
		const isActive = ['queued', 'running', 'suspended'].includes(r.status);
		edges = edges.map((e) => ({ ...e, animated: isActive }));
	}

	function watch() {
		unwatch?.();
		unwatch = watchRun(
			runId,
			(r) => {
				applyRun(r);
				error = '';
			},
			(e) => (error = e.message)
		);
	}

	// Fetch the initial snapshot from Postgres so the page renders even when the
	// SSE stream is unavailable (404, API restart, buffering reverse proxy).
	// Only layer live updates on top for still-running runs.
	async function load() {
		try {
			const r = await api.getRun(runId);
			applyRun(r);
			error = '';
			loaded = true;
			if (['queued', 'running', 'suspended'].includes(r.status)) watch();
		} catch (e) {
			loaded = true;
			if (e instanceof ApiError && e.status === 404) {
				notFound = true;
				return;
			}
			error = e instanceof Error ? e.message : String(e);
			// Non-404: still attempt the live stream in case the API recovers.
			watch();
		}
	}

	onMount(() => {
		load();
		return () => unwatch?.();
	});

	async function cancel() {
		try {
			await api.cancelRun(runId);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function retry() {
		try {
			await api.retryRun(runId);
			watch();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	function fmt(v: unknown): string {
		if (v == null) return '—';
		const s = typeof v === 'string' ? v : JSON.stringify(v, null, 2);
		return s.length > 2000 ? s.slice(0, 2000) + '…' : s;
	}

	// Suspension detail for a suspended task. A `wait.callback` step exposes a
	// resume_url (+ optional note) in its output; a long `delay` exposes only
	// waited_seconds, so we can only say it's timed.
	interface SuspendInfo {
		kind: 'callback' | 'timed';
		resumeUrl?: string;
		note?: string;
	}
	function suspendInfo(task: TaskInfo): SuspendInfo | null {
		if (task.status !== 'suspended') return null;
		const o = task.output;
		if (o && typeof o === 'object') {
			const rec = o as Record<string, unknown>;
			if (typeof rec.resume_url === 'string') {
				return {
					kind: 'callback',
					resumeUrl: `${API_BASE}${rec.resume_url}`,
					note: typeof rec.note === 'string' && rec.note ? rec.note : undefined
				};
			}
		}
		return { kind: 'timed' };
	}

	let copied = $state<string | null>(null);
	let copyTimer: ReturnType<typeof setTimeout> | null = null;
	async function copy(value: string) {
		try {
			await navigator.clipboard.writeText(value);
			copied = value;
			if (copyTimer) clearTimeout(copyTimer);
			copyTimer = setTimeout(() => (copied = null), 1500);
		} catch {
			/* clipboard unavailable — no-op */
		}
	}
</script>

{#snippet attemptCard(task: TaskInfo)}
	{@const susp = suspendInfo(task)}
	<div class="bg-card rounded-lg border p-3">
		<div class="flex items-center justify-between">
			<span class="text-muted-foreground text-xs font-medium">Attempt {task.attempt}</span>
			<span class="flex items-center gap-2">
				<span class="text-muted-foreground text-[10px] tabular-nums">
					{fmtDuration(task.started_at, task.finished_at)}
				</span>
				<Badge class={statusBadges[task.status] ?? ''} variant="outline">{task.status}</Badge>
			</span>
		</div>
		{#if task.error?.message}
			<pre
				class="bg-destructive/10 text-destructive mt-2 overflow-x-auto rounded p-2 text-[11px] whitespace-pre-wrap">{task
					.error.message}</pre>
		{/if}
		{#if susp}
			<div class="mt-2 rounded-md border border-amber-300 bg-amber-50 p-2.5 dark:border-amber-800 dark:bg-amber-950/50">
				<div class="flex items-center gap-1.5 text-xs font-medium text-amber-700 dark:text-amber-300">
					<PauseIcon class="size-3.5" />
					{susp.kind === 'callback' ? 'Waiting for callback' : 'Waiting (timed)'}
				</div>
				{#if susp.note}
					<p class="mt-1.5 text-xs text-amber-800/90 dark:text-amber-200/80">{susp.note}</p>
				{/if}
				{#if susp.resumeUrl}
					<div class="text-muted-foreground mt-2 text-[10px] font-medium tracking-wide uppercase">
						Resume URL
					</div>
					<div class="bg-background/70 mt-1 flex items-center gap-2 rounded border px-2 py-1">
						<code class="text-foreground min-w-0 flex-1 break-all font-mono text-[11px]">{susp.resumeUrl}</code>
						<Button
							variant="ghost"
							size="icon"
							class="size-6 shrink-0"
							onclick={() => copy(susp.resumeUrl!)}
							aria-label="Copy resume URL"
						>
							{#if copied === susp.resumeUrl}
								<CheckIcon class="size-3.5 text-emerald-500" />
							{:else}
								<CopyIcon class="size-3.5" />
							{/if}
						</Button>
					</div>
					<p class="text-muted-foreground/80 mt-1.5 text-[10px]">
						Send a POST here to resume this step.
					</p>
				{/if}
			</div>
		{:else if task.output != null}
			<pre
				class="bg-muted text-muted-foreground mt-2 max-h-64 overflow-auto rounded p-2 text-[11px] whitespace-pre-wrap">{fmt(
					task.output
				)}</pre>
		{/if}
	</div>
{/snippet}

{#if notFound}
	<div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
		<SearchXIcon class="text-muted-foreground/50 size-10" />
		<div>
			<div class="text-lg font-semibold">Run not found</div>
			<p class="text-muted-foreground mt-1 text-sm">
				This run may have been deleted, or the link is wrong.
			</p>
		</div>
		<Button variant="outline" href="/workflows">Back to workflows</Button>
	</div>
{:else}
	<div class="flex h-full min-h-0 flex-col">
		<div class="bg-background flex h-14 shrink-0 items-center gap-3 border-b px-4">
			<Button
				variant="ghost"
				size="icon"
				href={run ? `/workflows/${run.workflow_id}/runs` : '/'}
				aria-label="Back to run history"
			>
				<ArrowLeftIcon class="size-4" />
			</Button>
			<div class="min-w-0">
				<div class="flex items-center gap-2">
					<span class="truncate font-medium">{run?.workflow_name ?? '…'}</span>
					<Badge variant="secondary">v{run?.version ?? '—'}</Badge>
					{#if run}
						<Badge class={statusBadges[run.status] ?? ''} variant="outline">{run.status}</Badge>
					{/if}
				</div>
				<div class="text-muted-foreground text-xs">
					run <span class="font-mono">{runId.slice(0, 8)}</span>
					{#if run?.started_at}
						· {fmtRelative(run.started_at)} · {fmtDuration(run.started_at, run.finished_at)}
						· {fmtTime(run.started_at)}
					{/if}
				</div>
			</div>
			<div class="ml-auto flex gap-2">
				{#if run}
					<Button variant="ghost" size="icon" href="/workflows/{run.workflow_id}" aria-label="Edit workflow">
						<PencilIcon class="size-4" />
					</Button>
				{/if}
				{#if active}
					<Button variant="outline" class="text-destructive" onclick={cancel}>
						<BanIcon class="size-4" /> Cancel
					</Button>
				{:else if retryable}
					<Button variant="outline" onclick={retry}>
						<RotateCcwIcon class="size-4" /> Retry failed steps
					</Button>
				{/if}
			</div>
		</div>

		{#if error}
			<div class="border-destructive/30 bg-destructive/10 text-destructive border-b px-4 py-2 text-sm">
				{error}
			</div>
		{/if}

		<div class="relative flex min-h-0 flex-1">
			<div class="relative min-w-0 flex-1">
				<SvelteFlowProvider>
					<SvelteFlow
						bind:nodes
						bind:edges
						{nodeTypes}
						{colorMode}
						fitView
						nodesDraggable={false}
						nodesConnectable={false}
						deleteKey={null}
						onnodeclick={({ node }) => (selectedStep = node.id)}
						onpaneclick={() => (selectedStep = null)}
						proOptions={{ hideAttribution: true }}
					>
						<Background />
						<Controls showLock={false} />
						<MiniMap />
					</SvelteFlow>
				</SvelteFlowProvider>
			</div>

			<aside class="bg-background flex w-96 shrink-0 flex-col overflow-y-auto border-l">
				{#if selectedStep}
					<div class="flex items-center justify-between p-3">
						<div>
							<div class="font-mono text-sm font-medium">{selectedStep}</div>
							<div class="text-muted-foreground text-xs">
								{selectedAttempts.length}
								{selectedAttempts.length === 1 ? 'attempt' : 'attempts'}
							</div>
						</div>
						<Button variant="ghost" size="icon" class="size-7" onclick={() => (selectedStep = null)} aria-label="Close">
							<XIcon class="size-4" />
						</Button>
					</div>
					<Separator />
					<div class="space-y-3 p-3">
						{#each selectedAttempts as task (task.id)}
							{@render attemptCard(task)}
						{:else}
							<p class="text-muted-foreground text-sm">This step has not started yet.</p>
						{/each}
					</div>
				{:else if run}
					<div class="flex items-center gap-1 p-2">
						<Button
							variant={tab === 'tasks' ? 'secondary' : 'ghost'}
							size="sm"
							class="h-7 flex-1"
							onclick={() => (tab = 'tasks')}
						>
							Tasks
						</Button>
						<Button
							variant={tab === 'logs' ? 'secondary' : 'ghost'}
							size="sm"
							class="h-7 flex-1"
							onclick={() => (tab = 'logs')}
						>
							Logs
						</Button>
					</div>
					<Separator />
					{#if tab === 'logs'}
						<RunLogs runId={run.id} {active} />
					{:else}
						{#if hasInput}
							<div class="border-b p-3">
								<div class="text-muted-foreground mb-1.5 text-xs font-medium">Input</div>
								<pre
									class="bg-muted text-muted-foreground max-h-40 overflow-auto rounded p-2 text-[11px] whitespace-pre-wrap">{fmt(
										run.input
									)}</pre>
							</div>
						{/if}
						<div class="p-3 pb-2">
							<p class="text-muted-foreground/70 text-xs">
								Click a node on the canvas for attempt details.
							</p>
						</div>
						<div class="divide-border divide-y">
							{#each run.tasks as task (task.id)}
								<button
									class="hover:bg-muted/50 flex w-full items-center justify-between px-3 py-2.5 text-left"
									onclick={() => (selectedStep = task.step_key)}
								>
									<span class="font-mono text-xs font-medium">
										{task.step_key}{task.attempt > 1 ? ` #${task.attempt}` : ''}
									</span>
									<span class="flex items-center gap-2">
										<span class="text-muted-foreground text-[10px] tabular-nums">
											{fmtDuration(task.started_at, task.finished_at)}
										</span>
										<Badge class={statusBadges[task.status] ?? ''} variant="outline">{task.status}</Badge>
									</span>
								</button>
							{:else}
								<div class="text-muted-foreground px-3 py-6 text-center text-sm">No tasks yet…</div>
							{/each}
						</div>
					{/if}
				{:else}
					<div class="text-muted-foreground p-6 text-center text-sm">
						{loaded ? 'No run data.' : 'Loading…'}
					</div>
				{/if}
			</aside>
		</div>
	</div>
{/if}
