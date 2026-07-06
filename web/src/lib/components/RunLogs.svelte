<script lang="ts">
	import { api, runLogsDownloadUrl, type LogLine } from '$lib/api';
	import { Button } from '$lib/components/ui/button/index.js';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import LoaderIcon from '@lucide/svelte/icons/loader';

	let {
		runId,
		active = false,
		compact = false
	}: { runId: string; active?: boolean; compact?: boolean } = $props();

	// Page smaller than the server default (500) so "load older" is reachable on
	// runs with a lot of output, while a head window this size still tails fine.
	const PAGE = 200;

	let logs = $state<LogLine[]>([]);
	let loaded = $state(false);
	let moreOlder = $state(false); // older lines exist beyond the loaded window
	let loadingOlder = $state(false);
	let pagedBack = $state(false); // user has loaded at least one older page

	// Refresh the newest window (the live tail). Merge it over the loaded set so
	// any older pages the user paged back to stay put across polls.
	async function refresh() {
		try {
			const head = await api.runLogs(runId, { limit: PAGE }); // newest first
			const minHead = head.length ? head[head.length - 1].id : Infinity;
			const older = logs.filter((l) => l.id < minHead);
			logs = [...head, ...older];
			// The head page alone establishes whether older lines exist — until the
			// user pages back, after which loadOlder() owns the flag.
			if (!pagedBack) moreOlder = head.length === PAGE;
		} catch {
			/* transient; next poll retries */
		} finally {
			loaded = true;
		}
	}

	// Page backward from the oldest loaded line; older lines append at the bottom
	// (the list is newest-first). Works for live and terminal runs alike.
	async function loadOlder() {
		if (loadingOlder || logs.length === 0) return;
		loadingOlder = true;
		pagedBack = true;
		try {
			const before_id = logs[logs.length - 1].id;
			const older = await api.runLogs(runId, { limit: PAGE, before_id });
			const seen = new Set(logs.map((l) => l.id));
			logs = [...logs, ...older.filter((l) => !seen.has(l.id))];
			moreOlder = older.length === PAGE;
		} catch {
			/* transient; the button stays for a retry */
		} finally {
			loadingOlder = false;
		}
	}

	// Initial load per run + 2s poll while the run is live.
	$effect(() => {
		void runId;
		loaded = false;
		moreOlder = false;
		pagedBack = false;
		logs = [];
		refresh();
	});
	// Poll while live, and do one final refresh on the active→terminal
	// transition so the tail (usually the failure lines) is never missed.
	let wasActive = false;
	$effect(() => {
		if (active) {
			wasActive = true;
			const t = setInterval(refresh, 2000);
			return () => clearInterval(t);
		}
		if (wasActive) {
			wasActive = false;
			refresh();
		}
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
		{#if moreOlder && logs.length > 0}
			<div class="p-2 text-center">
				<Button
					variant="ghost"
					size="sm"
					class="text-muted-foreground h-7"
					onclick={loadOlder}
					disabled={loadingOlder}
				>
					{#if loadingOlder}
						<LoaderIcon class="size-3.5 animate-spin" /> Loading…
					{:else}
						<ChevronDownIcon class="size-3.5" /> Load older lines
					{/if}
				</Button>
			</div>
		{/if}
	</div>
</div>
