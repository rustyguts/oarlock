<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, type RunSummary, type Workflow } from '$lib/api';
	import { statusBadges, fmtDuration, fmtRelative } from '$lib/flow';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import CheckCircle2Icon from '@lucide/svelte/icons/check-circle-2';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import BanIcon from '@lucide/svelte/icons/ban';
	import LoaderIcon from '@lucide/svelte/icons/loader';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ActivityIcon from '@lucide/svelte/icons/activity';
	import PercentIcon from '@lucide/svelte/icons/percent';
	import TimerIcon from '@lucide/svelte/icons/timer';
	import ZapIcon from '@lucide/svelte/icons/zap';

	const workflowId = page.params.id!;
	let workflow = $state<Workflow | null>(null);
	let runs = $state<RunSummary[]>([]);
	let error = $state('');

	let failurePct = $derived(
		workflow && workflow.run_count > 0
			? ((workflow.failed_count / workflow.run_count) * 100).toFixed(0)
			: null
	);
	let running = $derived(
		runs.filter((r) => ['queued', 'running', 'suspended'].includes(r.status)).length
	);
	let avgDuration = $derived.by(() => {
		const done = runs.filter((r) => r.started_at && r.finished_at);
		if (!done.length) return '—';
		const avg =
			done.reduce(
				(sum, r) =>
					sum +
					(new Date(r.finished_at!.replace(' ', 'T').replace(/([+-]\d\d)$/, '$1:00')).getTime() -
						new Date(r.started_at!.replace(' ', 'T').replace(/([+-]\d\d)$/, '$1:00')).getTime()),
				0
			) / done.length;
		return avg < 1000 ? `${Math.round(avg)}ms` : `${(avg / 1000).toFixed(1)}s`;
	});

	async function refresh() {
		try {
			const [wfs, rs] = await Promise.all([api.listWorkflows(), api.listRuns(workflowId)]);
			workflow = wfs.find((w) => w.id === workflowId) ?? null;
			runs = rs;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	onMount(() => {
		refresh();
		const t = setInterval(refresh, 4000);
		return () => clearInterval(t);
	});
</script>

{#snippet statusIcon(status: string)}
	{#if status === 'succeeded'}
		<CheckCircle2Icon class="size-4 text-emerald-500" />
	{:else if status === 'failed'}
		<XCircleIcon class="size-4 text-red-500" />
	{:else if status === 'canceled'}
		<BanIcon class="text-muted-foreground size-4" />
	{:else if status === 'running'}
		<LoaderIcon class="size-4 animate-spin text-blue-500" />
	{:else}
		<ClockIcon class="text-muted-foreground size-4" />
	{/if}
{/snippet}

<div class="w-full px-6 py-6">
	<div class="mb-6 flex items-center gap-3">
		<Button variant="ghost" size="icon" href="/workflows" aria-label="Back to workflows">
			<ArrowLeftIcon class="size-4" />
		</Button>
		<div>
			<h1 class="text-xl font-semibold">{workflow?.name ?? '…'}</h1>
			<p class="text-muted-foreground text-xs">Run history</p>
		</div>
		<Button variant="outline" href="/workflows/{workflowId}" class="ml-auto">
			<PencilIcon class="size-4" /> Open in editor
		</Button>
	</div>

	<div class="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
		<Card.Root class="gap-1 py-4">
			<Card.Content class="px-4">
				<div class="text-muted-foreground flex items-center gap-1.5 text-xs">
					<ActivityIcon class="size-3.5" /> Total runs
				</div>
				<div class="mt-1 text-2xl font-semibold tabular-nums">{workflow?.run_count ?? '—'}</div>
			</Card.Content>
		</Card.Root>
		<Card.Root class="gap-1 py-4">
			<Card.Content class="px-4">
				<div class="text-muted-foreground flex items-center gap-1.5 text-xs">
					<PercentIcon class="size-3.5" /> Failure rate
				</div>
				<div
					class="mt-1 text-2xl font-semibold tabular-nums
					{failurePct === null
						? ''
						: Number(failurePct) > 0
							? 'text-red-600 dark:text-red-400'
							: 'text-emerald-600 dark:text-emerald-400'}"
				>
					{failurePct === null ? '—' : `${failurePct}%`}
				</div>
			</Card.Content>
		</Card.Root>
		<Card.Root class="gap-1 py-4">
			<Card.Content class="px-4">
				<div class="text-muted-foreground flex items-center gap-1.5 text-xs">
					<TimerIcon class="size-3.5" /> Avg duration
				</div>
				<div class="mt-1 text-2xl font-semibold tabular-nums">{avgDuration}</div>
			</Card.Content>
		</Card.Root>
		<Card.Root class="gap-1 py-4">
			<Card.Content class="px-4">
				<div class="text-muted-foreground flex items-center gap-1.5 text-xs">
					<ZapIcon class="size-3.5" /> Active now
				</div>
				<div class="mt-1 text-2xl font-semibold tabular-nums {running > 0 ? 'text-blue-500' : ''}">
					{running}
				</div>
			</Card.Content>
		</Card.Root>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	{#if runs.length === 0}
		<Card.Root class="border-dashed">
			<Card.Content class="text-muted-foreground py-10 text-center">
				No runs yet. Open the editor and hit Run.
			</Card.Content>
		</Card.Root>
	{:else}
		<Card.Root class="py-0">
			<Card.Content class="divide-border divide-y px-0">
				{#each runs as run (run.id)}
					<a
						href="/runs/{run.id}"
						class="hover:bg-muted/50 group flex items-center gap-3 px-4 py-3 transition-colors"
					>
						{@render statusIcon(run.status)}
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2">
								<span class="font-mono text-sm font-medium">{run.id.slice(0, 8)}</span>
								<Badge class={statusBadges[run.status] ?? ''} variant="outline">{run.status}</Badge>
								<Badge variant="secondary">v{run.version}</Badge>
							</div>
							{#if run.error_summary}
								<div class="text-destructive mt-0.5 truncate text-xs">{run.error_summary}</div>
							{/if}
						</div>
						<div class="text-muted-foreground shrink-0 text-right text-xs">
							<div>{fmtRelative(run.started_at ?? run.created_at)}</div>
							<div class="tabular-nums">
								{fmtDuration(run.started_at, run.finished_at)} · {run.task_count}
								{run.task_count === 1 ? 'task' : 'tasks'}
							</div>
						</div>
						<ChevronRightIcon
							class="text-muted-foreground/40 group-hover:text-muted-foreground size-4 shrink-0 transition-colors"
						/>
					</a>
				{/each}
			</Card.Content>
		</Card.Root>
	{/if}
</div>
