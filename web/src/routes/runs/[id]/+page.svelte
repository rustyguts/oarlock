<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
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

	import { api, watchRun, type Run, type TaskInfo } from '$lib/api';
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

	const runId = page.params.id!;
	const nodeTypes: NodeTypes = { step: StepNodeView };

	let run = $state<Run | null>(null);
	let nodes = $state.raw<StepNode[]>([]);
	let edges = $state.raw<Edge[]>([]);
	let selectedStep = $state<string | null>(null);
	let tab = $state<'tasks' | 'logs'>('tasks');
	let error = $state('');
	let unwatch: (() => void) | null = null;

	let active = $derived(run ? ['queued', 'running', 'suspended'].includes(run.status) : false);
	let retryable = $derived(run ? ['failed', 'canceled'].includes(run.status) : false);
	let colorMode = $derived((mode.current ?? 'light') as ColorMode);

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

	onMount(() => {
		watch();
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
</script>

{#snippet attemptCard(task: TaskInfo)}
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
		{#if task.output != null}
			<pre
				class="bg-muted text-muted-foreground mt-2 max-h-64 overflow-auto rounded p-2 text-[11px] whitespace-pre-wrap">{fmt(
					task.output
				)}</pre>
		{/if}
	</div>
{/snippet}

<div class="flex h-full min-h-0 flex-col">
	<div class="bg-background flex h-14 shrink-0 items-center gap-3 border-b px-4 max-lg:h-auto max-lg:min-h-14 max-lg:flex-wrap max-lg:py-2">
		<Button
			variant="ghost"
			size="icon"
			href={run ? `/workflows/${run.workflow_id}/runs` : '/'}
			aria-label="Back to run history"
		>
			<ArrowLeftIcon class="size-4" />
		</Button>
		<div class="min-w-0 flex-1">
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
					<span class="hidden sm:inline">· {fmtTime(run.started_at)}</span>
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
		<div class="relative min-w-0 flex-1 max-lg:hidden">
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

		<aside class="bg-background flex shrink-0 flex-col overflow-y-auto border-l max-lg:w-full max-lg:border-l-0 lg:w-96">
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
				<div class="text-muted-foreground p-6 text-center text-sm">Loading…</div>
			{/if}
		</aside>
	</div>
</div>
