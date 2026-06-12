<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type Workflow } from '$lib/api';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import HistoryIcon from '@lucide/svelte/icons/history';
	import PlusIcon from '@lucide/svelte/icons/plus';

	let workflows = $state<Workflow[]>([]);
	let newName = $state('');
	let error = $state('');
	let loading = $state(true);

	async function refresh() {
		try {
			workflows = await api.listWorkflows();
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (!newName.trim()) return;
		try {
			const { id } = await api.createWorkflow(newName.trim());
			await goto(`/workflows/${id}`);
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	async function remove(id: string) {
		if (!confirm('Delete this workflow and all its runs?')) return;
		await api.deleteWorkflow(id);
		await refresh();
	}

	onMount(refresh);
</script>

<div class="w-full px-6 py-6">
	<div class="mb-6 flex items-center justify-between gap-4">
		<h1 class="text-xl font-semibold">Workflows</h1>
		<form onsubmit={create} class="flex gap-2">
			<Input bind:value={newName} placeholder="New workflow name…" class="w-56" />
			<Button type="submit"><PlusIcon class="size-4" /> Create</Button>
		</form>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error} — is the API running on port 9000?
		</div>
	{/if}

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if workflows.length === 0}
		<Card.Root class="border-dashed">
			<Card.Content class="text-muted-foreground py-10 text-center">
				No workflows yet. Create one to open the canvas.
			</Card.Content>
		</Card.Root>
	{:else}
		<Card.Root class="py-0">
			<Card.Content class="divide-border divide-y px-0">
				{#each workflows as wf (wf.id)}
					<div class="hover:bg-muted/50 flex items-center justify-between px-4 py-3">
						<a href="/workflows/{wf.id}" class="min-w-0 flex-1">
							<div class="flex items-center gap-2 font-medium">
								{wf.name}
								<Badge variant="secondary">v{wf.version ?? '—'}</Badge>
							</div>
							<div class="text-muted-foreground mt-0.5 flex items-center gap-2 text-xs">
								<span>{wf.slug}</span>
								<span>·</span>
								<span>{wf.run_count} {wf.run_count === 1 ? 'run' : 'runs'}</span>
								{#if wf.run_count > 0}
									<span>·</span>
									<span
										class={wf.failed_count > 0
											? 'text-red-600 dark:text-red-400'
											: 'text-emerald-600 dark:text-emerald-400'}
									>
										{((wf.failed_count / wf.run_count) * 100).toFixed(0)}% failed
									</span>
								{/if}
							</div>
						</a>
						<div class="ml-4 flex items-center gap-1">
							<Button variant="outline" size="sm" href="/workflows/{wf.id}/runs">
								<HistoryIcon class="size-4" /> Runs
							</Button>
							<Button
								variant="ghost"
								size="icon"
								class="text-muted-foreground hover:text-destructive"
								onclick={() => remove(wf.id)}
								aria-label="Delete workflow"
							>
								<Trash2Icon class="size-4" />
							</Button>
						</div>
					</div>
				{/each}
			</Card.Content>
		</Card.Root>
	{/if}
</div>
