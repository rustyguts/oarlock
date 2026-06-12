<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Stats } from '$lib/api';
	import { statusBadges, fmtRelative, fmtDuration } from '$lib/flow';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import ActivityIcon from '@lucide/svelte/icons/activity';
	import CircleCheckBigIcon from '@lucide/svelte/icons/circle-check-big';
	import CircleXIcon from '@lucide/svelte/icons/circle-x';
	import TimerIcon from '@lucide/svelte/icons/timer';
	import ZapIcon from '@lucide/svelte/icons/zap';
	import WorkflowIcon from '@lucide/svelte/icons/workflow';
	import ServerIcon from '@lucide/svelte/icons/server';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import ScrollTextIcon from '@lucide/svelte/icons/scroll-text';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';

	let stats = $state<Stats | null>(null);
	let error = $state('');

	async function refresh() {
		try {
			stats = await api.stats();
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}
	onMount(() => {
		refresh();
		const t = setInterval(refresh, 10_000);
		return () => clearInterval(t);
	});

	let maxDaily = $derived(
		stats ? Math.max(1, ...stats.daily.map((d) => d.succeeded + d.failed + d.canceled)) : 1
	);

	function fmtMS(ms: number | null): string {
		if (ms == null) return '—';
		if (ms < 1000) return `${ms}ms`;
		if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
		return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
	}

	function dayLabel(date: string): string {
		return date.slice(8, 10);
	}

	// Donut segments from task statuses.
	const donutColors: Record<string, string> = {
		succeeded: 'var(--color-emerald-500)',
		failed: 'var(--color-red-500)',
		canceled: 'var(--color-zinc-400)',
		running: 'var(--color-blue-500)',
		queued: 'var(--color-amber-400)',
		skipped: 'var(--color-zinc-300)',
		suspended: 'var(--color-amber-500)'
	};
	let donut = $derived.by(() => {
		if (!stats) return { segments: [], total: 0 };
		const entries = Object.entries(stats.task_statuses).filter(([, n]) => n > 0);
		const total = entries.reduce((s, [, n]) => s + n, 0);
		let acc = 0;
		const C = 2 * Math.PI * 15.915; // r chosen so circumference ≈ 100
		const segments = entries.map(([status, n]) => {
			const frac = n / total;
			const seg = { status, n, dash: `${frac * C} ${C - frac * C}`, offset: -acc * C };
			acc += frac;
			return seg;
		});
		return { segments, total };
	});

	let failurePct = $derived(
		stats && stats.totals.runs > 0
			? Math.round((stats.totals.failed / stats.totals.runs) * 100)
			: null
	);
</script>

{#snippet statCard(Icon: typeof ActivityIcon, label: string, value: string, accent: string, sub: string)}
	<Card.Root class="gap-0 py-4">
		<Card.Content class="px-4">
			<div class="flex items-center justify-between">
				<span class="text-muted-foreground text-xs font-medium">{label}</span>
				<span class="flex size-7 items-center justify-center rounded-md {accent}">
					<Icon class="size-4" />
				</span>
			</div>
			<div class="mt-1 text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
			<div class="text-muted-foreground/70 mt-0.5 text-xs">{sub}</div>
		</Card.Content>
	</Card.Root>
{/snippet}

<div class="flex h-full w-full flex-col gap-3 overflow-hidden px-6 py-4">
	<div class="flex shrink-0 items-center justify-between">
		<div>
			<h1 class="text-xl font-semibold tracking-tight">Dashboard</h1>
			<p class="text-muted-foreground text-sm">Workspace activity at a glance.</p>
		</div>
		<Button href="/workflows">Workflows <ArrowRightIcon class="size-4" /></Button>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	{#if stats}
		<!-- stat cards -->
		<div class="grid shrink-0 grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
			{@render statCard(ActivityIcon, 'Total runs', String(stats.totals.runs), 'bg-primary/15 text-primary-strong', `${stats.totals.tasks} tasks executed`)}
			{@render statCard(
				CircleCheckBigIcon,
				'Success rate',
				stats.totals.success_rate == null ? '—' : `${Math.round(stats.totals.success_rate * 100)}%`,
				'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
				`${stats.totals.succeeded} succeeded`
			)}
			{@render statCard(
				CircleXIcon,
				'Failed',
				String(stats.totals.failed),
				'bg-red-500/15 text-red-600 dark:text-red-400',
				failurePct == null ? '—' : `${failurePct}% of all runs`
			)}
			{@render statCard(TimerIcon, 'Avg duration', fmtMS(stats.totals.avg_duration_ms), 'bg-blue-500/15 text-blue-600 dark:text-blue-400', 'across finished runs')}
			{@render statCard(
				ZapIcon,
				'Active now',
				String(stats.totals.active),
				'bg-amber-500/15 text-amber-600 dark:text-amber-400',
				stats.totals.active > 0 ? 'runs in flight' : 'all quiet'
			)}
			{@render statCard(WorkflowIcon, 'Workflows', String(stats.totals.workflows), 'bg-violet-500/15 text-violet-600 dark:text-violet-400', `${stats.totals.mcp_servers} MCP · ${stats.totals.secrets} secrets`)}
		</div>

		<div class="grid min-h-0 flex-[4] gap-3 lg:grid-cols-3">
			<!-- runs over time -->
			<Card.Root class="h-full min-h-0 gap-0 py-4 lg:col-span-2">
				<Card.Content class="flex h-full min-h-0 flex-col px-4">
					<div class="mb-4 flex items-baseline justify-between">
						<h2 class="text-sm font-semibold">Runs — last 14 days</h2>
						<div class="text-muted-foreground flex items-center gap-3 text-xs">
							<span class="flex items-center gap-1"><span class="size-2 rounded-sm bg-emerald-500"></span> succeeded</span>
							<span class="flex items-center gap-1"><span class="size-2 rounded-sm bg-red-500"></span> failed</span>
							<span class="flex items-center gap-1"><span class="size-2 rounded-sm bg-zinc-400"></span> canceled</span>
						</div>
					</div>
					<div class="flex min-h-0 flex-1 items-end gap-1.5">
						{#each stats.daily as d (d.date)}
							{@const total = d.succeeded + d.failed + d.canceled}
							<div class="group flex h-full flex-1 flex-col justify-end" title="{d.date}: {d.succeeded} ok · {d.failed} failed · {d.canceled} canceled">
								<div class="flex w-full flex-col justify-end gap-px" style="height: {(total / maxDaily) * 100}%">
									{#if d.canceled}<div class="w-full rounded-sm bg-zinc-400/80" style="height: {(d.canceled / Math.max(total, 1)) * 100}%"></div>{/if}
									{#if d.failed}<div class="w-full rounded-sm bg-red-500/90" style="height: {(d.failed / Math.max(total, 1)) * 100}%"></div>{/if}
									{#if d.succeeded}<div class="w-full rounded-sm bg-emerald-500/90 group-hover:bg-emerald-400" style="height: {(d.succeeded / Math.max(total, 1)) * 100}%"></div>{/if}
								</div>
								{#if total === 0}<div class="bg-muted h-1 w-full rounded-sm"></div>{/if}
							</div>
						{/each}
					</div>
					<div class="text-muted-foreground/60 mt-2 flex shrink-0 gap-1.5 text-[10px] tabular-nums">
						{#each stats.daily as d (d.date)}
							<div class="flex-1 text-center">{dayLabel(d.date)}</div>
						{/each}
					</div>
				</Card.Content>
			</Card.Root>

			<!-- task status donut -->
			<Card.Root class="h-full min-h-0 gap-0 py-4">
				<Card.Content class="flex h-full min-h-0 flex-col justify-center px-4">
					<h2 class="mb-4 text-sm font-semibold">Task outcomes</h2>
					<div class="flex items-center gap-5">
						<svg viewBox="0 0 42 42" class="size-32 shrink-0 -rotate-90">
							<circle cx="21" cy="21" r="15.915" fill="none" class="stroke-muted" stroke-width="5" />
							{#each donut.segments as seg (seg.status)}
								<circle
									cx="21" cy="21" r="15.915" fill="none"
									stroke={donutColors[seg.status] ?? 'var(--color-zinc-400)'}
									stroke-width="5"
									stroke-dasharray={seg.dash}
									stroke-dashoffset={seg.offset}
								/>
							{/each}
							<text x="21" y="21" text-anchor="middle" dominant-baseline="central"
								class="fill-foreground rotate-90 text-[8px] font-semibold" transform-origin="21 21">
								{donut.total}
							</text>
						</svg>
						<ul class="min-w-0 flex-1 space-y-1.5">
							{#each donut.segments as seg (seg.status)}
								<li class="flex items-center justify-between text-xs">
									<span class="flex items-center gap-1.5">
										<span class="size-2 rounded-full" style="background: {donutColors[seg.status] ?? 'var(--color-zinc-400)'}"></span>
										{seg.status}
									</span>
									<span class="text-muted-foreground tabular-nums">
										{seg.n} · {Math.round((seg.n / donut.total) * 100)}%
									</span>
								</li>
							{/each}
						</ul>
					</div>
					<div class="text-muted-foreground/70 border-t pt-3 mt-4 flex items-center justify-between text-xs">
						<span class="flex items-center gap-1.5"><ScrollTextIcon class="size-3.5" /> {stats.totals.log_lines.toLocaleString()} log lines captured</span>
					</div>
				</Card.Content>
			</Card.Root>
		</div>

		<div class="grid min-h-0 flex-[5] gap-3 lg:grid-cols-2">
			<!-- top workflows -->
			<Card.Root class="h-full min-h-0 gap-0 py-0">
				<Card.Content class="flex h-full min-h-0 flex-col px-0">
					<div class="flex shrink-0 items-center justify-between px-4 py-3">
						<h2 class="text-sm font-semibold">Top workflows</h2>
						<a href="/workflows" class="text-muted-foreground hover:text-foreground text-xs">View all</a>
					</div>
					<div class="divide-border min-h-0 flex-1 divide-y overflow-y-auto border-t">
						{#each stats.top_workflows.filter((w) => w.runs > 0) as wf (wf.id)}
							{@const pct = wf.runs ? Math.round((wf.failed / wf.runs) * 100) : 0}
							<a href="/workflows/{wf.id}/runs" class="hover:bg-muted/40 flex items-center gap-3 px-4 py-2.5">
								<div class="min-w-0 flex-1">
									<div class="flex items-center justify-between gap-2">
										<span class="truncate text-sm font-medium">{wf.name}</span>
										<span class="text-muted-foreground shrink-0 text-xs tabular-nums">
											{wf.runs} {wf.runs === 1 ? 'run' : 'runs'} · {fmtMS(wf.avg_duration_ms)}
										</span>
									</div>
									<div class="mt-1.5 flex items-center gap-2">
										<div class="bg-muted h-1.5 flex-1 overflow-hidden rounded-full">
											<div class="flex h-full">
												<div class="bg-emerald-500" style="width: {100 - pct}%"></div>
												<div class="bg-red-500" style="width: {pct}%"></div>
											</div>
										</div>
										<span class="shrink-0 text-[10px] tabular-nums {pct > 0 ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'}">
											{pct}% failed
										</span>
									</div>
								</div>
								<ChevronRightIcon class="text-muted-foreground/40 size-4 shrink-0" />
							</a>
						{/each}
					</div>
				</Card.Content>
			</Card.Root>

			<!-- recent activity -->
			<Card.Root class="h-full min-h-0 gap-0 py-0">
				<Card.Content class="flex h-full min-h-0 flex-col px-0">
					<div class="flex shrink-0 items-center justify-between px-4 py-3">
						<h2 class="text-sm font-semibold">Recent activity</h2>
						<span class="text-muted-foreground text-xs">auto-refreshes</span>
					</div>
					<div class="divide-border min-h-0 flex-1 divide-y overflow-y-auto border-t">
						{#each stats.recent_runs as run (run.id)}
							<a href="/runs/{run.id}" class="hover:bg-muted/40 flex items-center gap-3 px-4 py-2.5">
								<span class="size-2 shrink-0 rounded-full
									{run.status === 'succeeded' ? 'bg-emerald-500' : run.status === 'failed' ? 'bg-red-500' : run.status === 'running' ? 'animate-pulse bg-blue-500' : 'bg-zinc-400'}"
								></span>
								<div class="min-w-0 flex-1">
									<div class="flex items-center gap-2">
										<span class="truncate text-sm font-medium">{run.workflow_name}</span>
										<Badge class={statusBadges[run.status] ?? ''} variant="outline">{run.status}</Badge>
									</div>
								</div>
								<div class="text-muted-foreground shrink-0 text-right text-xs tabular-nums">
									<div>{fmtRelative(run.created_at)}</div>
									<div>{fmtDuration(run.started_at, run.finished_at)}</div>
								</div>
							</a>
						{/each}
					</div>
				</Card.Content>
			</Card.Root>
		</div>

		<!-- resources strip -->
		<div class="grid shrink-0 grid-cols-1 gap-3 sm:grid-cols-3">
			<a href="/workflows" class="hover:border-primary/50 flex items-center gap-3 rounded-xl border p-4 transition-colors">
				<span class="bg-primary/15 text-primary-strong flex size-9 items-center justify-center rounded-lg"><WorkflowIcon class="size-4.5" /></span>
				<div><div class="text-sm font-semibold">{stats.totals.workflows} workflows</div><div class="text-muted-foreground text-xs">build & run automations</div></div>
			</a>
			<a href="/mcp" class="hover:border-primary/50 flex items-center gap-3 rounded-xl border p-4 transition-colors">
				<span class="bg-blue-500/15 text-blue-600 dark:text-blue-400 flex size-9 items-center justify-center rounded-lg"><ServerIcon class="size-4.5" /></span>
				<div><div class="text-sm font-semibold">{stats.totals.mcp_servers} MCP servers</div><div class="text-muted-foreground text-xs">tools your flows can call</div></div>
			</a>
			<a href="/configuration" class="hover:border-primary/50 flex items-center gap-3 rounded-xl border p-4 transition-colors">
				<span class="bg-violet-500/15 text-violet-600 dark:text-violet-400 flex size-9 items-center justify-center rounded-lg"><ShieldIcon class="size-4.5" /></span>
				<div><div class="text-sm font-semibold">{stats.totals.secrets} secrets</div><div class="text-muted-foreground text-xs">encrypted, never logged</div></div>
			</a>
		</div>
	{:else if !error}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{/if}
</div>
