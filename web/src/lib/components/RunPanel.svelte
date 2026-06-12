<script lang="ts">
	import type { Run } from '$lib/api';
	import { statusBadges, fmtDuration } from '$lib/flow';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import RunLogs from './RunLogs.svelte';
	import XIcon from '@lucide/svelte/icons/x';
	import BanIcon from '@lucide/svelte/icons/ban';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';

	let {
		run,
		onclose,
		oncancel,
		onretry
	}: {
		run: Run;
		onclose: () => void;
		oncancel: () => void;
		onretry: () => void;
	} = $props();

	let active = $derived(['queued', 'running', 'suspended'].includes(run.status));
	let retryable = $derived(['failed', 'canceled'].includes(run.status));
	let tab = $state<'tasks' | 'logs'>('tasks');

	function fmt(v: unknown): string {
		if (v == null) return '—';
		const s = typeof v === 'string' ? v : JSON.stringify(v, null, 1);
		return s.length > 300 ? s.slice(0, 300) + '…' : s;
	}
</script>

<div
	class="bg-card absolute right-3 bottom-3 z-10 max-h-72 w-96 overflow-y-auto rounded-lg border shadow-lg"
>
	<div class="bg-card sticky top-0 flex items-center justify-between border-b px-3 py-2">
		<div class="flex items-center gap-2">
			<Badge class={statusBadges[run.status] ?? ''} variant="outline">{run.status}</Badge>
			<button
				class="text-xs font-semibold tracking-wide uppercase {tab === 'tasks'
					? ''
					: 'text-muted-foreground'}"
				onclick={() => (tab = 'tasks')}
			>
				Tasks
			</button>
			<button
				class="text-xs font-semibold tracking-wide uppercase {tab === 'logs'
					? ''
					: 'text-muted-foreground'}"
				onclick={() => (tab = 'logs')}
			>
				Logs
			</button>
			<a href="/runs/{run.id}" class="text-muted-foreground text-xs underline-offset-2 hover:underline">
				detail
			</a>
		</div>
		<div class="flex items-center gap-1">
			{#if active}
				<Button variant="ghost" size="sm" class="text-destructive h-7" onclick={oncancel}>
					<BanIcon class="size-3.5" /> Cancel
				</Button>
			{:else if retryable}
				<Button variant="ghost" size="sm" class="h-7" onclick={onretry}>
					<RotateCcwIcon class="size-3.5" /> Retry
				</Button>
			{/if}
			<Button variant="ghost" size="icon" class="size-7" onclick={onclose} aria-label="Close">
				<XIcon class="size-4" />
			</Button>
		</div>
	</div>
	{#if tab === 'logs'}
		<RunLogs runId={run.id} {active} compact />
	{:else}
	<div class="divide-border divide-y">
		{#each run.tasks as task (task.id)}
			<div class="px-3 py-2">
				<div class="flex items-center justify-between">
					<span class="font-mono text-xs font-medium">
						{task.step_key}{task.attempt > 1 ? ` #${task.attempt}` : ''}
					</span>
					<span class="flex items-center gap-2">
						<span class="text-muted-foreground text-[10px]">
							{fmtDuration(task.started_at, task.finished_at)}
						</span>
						<Badge class={statusBadges[task.status] ?? ''} variant="outline">{task.status}</Badge>
					</span>
				</div>
				{#if task.error?.message}
					<pre
						class="bg-destructive/10 text-destructive mt-1 overflow-x-auto rounded p-1.5 text-[10px] whitespace-pre-wrap">{task
							.error.message}</pre>
				{:else if task.output != null}
					<pre
						class="bg-muted text-muted-foreground mt-1 overflow-x-auto rounded p-1.5 text-[10px] whitespace-pre-wrap">{fmt(
							task.output
						)}</pre>
				{/if}
			</div>
		{:else}
			<div class="text-muted-foreground px-3 py-4 text-center text-xs">Waiting for tasks…</div>
		{/each}
	</div>
	{/if}
</div>
