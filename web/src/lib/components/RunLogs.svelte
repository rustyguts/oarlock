<script lang="ts">
	import { api, runLogsDownloadUrl, type LogLine } from '$lib/api';
	import { Button } from '$lib/components/ui/button/index.js';
	import DownloadIcon from '@lucide/svelte/icons/download';

	let {
		runId,
		active = false,
		compact = false
	}: { runId: string; active?: boolean; compact?: boolean } = $props();

	let logs = $state<LogLine[]>([]);
	let loaded = $state(false);

	async function refresh() {
		try {
			logs = await api.runLogs(runId); // newest first from the API
		} catch {
			/* transient; next poll retries */
		} finally {
			loaded = true;
		}
	}

	// Initial load per run + 2s poll while the run is live.
	$effect(() => {
		void runId;
		loaded = false;
		refresh();
	});
	$effect(() => {
		if (!active) return;
		const t = setInterval(refresh, 2000);
		return () => clearInterval(t);
	});

	function levelBadge(level: number): { label: string; cls: string } {
		if (level >= 8) return { label: 'ERR', cls: 'text-red-600 dark:text-red-400' };
		if (level >= 4) return { label: 'WRN', cls: 'text-amber-600 dark:text-amber-400' };
		return { label: 'INF', cls: 'text-muted-foreground' };
	}

	function fmtFields(f: Record<string, unknown> | null): string {
		if (!f || Object.keys(f).length === 0) return '';
		return Object.entries(f)
			.map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
			.join(' ');
	}
</script>

<div class="flex min-h-0 flex-col">
	<div class="flex items-center justify-between gap-2 px-3 py-2">
		<span class="text-muted-foreground text-xs">
			{logs.length} {logs.length === 1 ? 'line' : 'lines'} · newest first
		</span>
		<Button variant="outline" size="sm" class="h-7" href={runLogsDownloadUrl(runId)} download>
			<DownloadIcon class="size-3.5" /> Download
		</Button>
	</div>
	<div class="divide-border/50 bg-muted/30 divide-y overflow-y-auto font-mono {compact ? 'max-h-44' : ''}">
		{#each logs as line (line.id)}
			{@const lvl = levelBadge(line.level)}
			<div class="flex items-baseline gap-2 px-3 py-1 {compact ? 'text-[10px]' : 'text-[11px]'}">
				<span class="text-muted-foreground/60 shrink-0 tabular-nums">{line.ts.slice(11, 23)}</span>
				<span class="shrink-0 font-semibold {lvl.cls}">{lvl.label}</span>
				<span class="text-primary-foreground/0 shrink-0"></span>
				<span class="text-muted-foreground shrink-0">[{line.step_key}]</span>
				<span class="min-w-0 break-words">
					{line.message}
					{#if fmtFields(line.fields)}
						<span class="text-muted-foreground/70">{fmtFields(line.fields)}</span>
					{/if}
				</span>
			</div>
		{:else}
			<div class="text-muted-foreground px-3 py-6 text-center {compact ? 'text-xs' : 'text-sm'}">
				{loaded ? 'No log lines yet.' : 'Loading…'}
			</div>
		{/each}
	</div>
</div>
