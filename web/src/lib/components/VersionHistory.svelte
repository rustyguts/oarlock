<script lang="ts">
	import { api, type Definition, type WorkflowVersion } from '$lib/api';
	import { fmtRelative } from '$lib/flow';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import EyeIcon from '@lucide/svelte/icons/eye';
	import Undo2Icon from '@lucide/svelte/icons/undo-2';
	import GitCommitVerticalIcon from '@lucide/svelte/icons/git-commit-vertical';

	let {
		open = $bindable(false),
		workflowId,
		dirty,
		onrestore
	}: {
		open?: boolean;
		workflowId: string;
		dirty: boolean;
		// Editor owns the write: it saves the chosen definition as a new version and
		// swaps its canvas over. Resolves once the new version is live.
		onrestore: (definition: Definition, sourceVersion: number) => Promise<void>;
	} = $props();

	let versions = $state<WorkflowVersion[]>([]);
	let loading = $state(false);
	let error = $state('');

	// JSON preview dialog
	let viewOpen = $state(false);
	let viewVersion = $state<number | null>(null);
	let viewJson = $state('');
	let viewLoading = $state(false);
	let viewError = $state('');

	// Restore confirm
	let confirmOpen = $state(false);
	let pendingRestore = $state<number | null>(null);
	let restoring = $state(false);

	async function load() {
		loading = true;
		error = '';
		try {
			versions = await api.listVersions(workflowId);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// Refetch each time the sheet opens so the list reflects saves made since.
	$effect(() => {
		if (open) load();
	});

	async function view(version: number) {
		viewVersion = version;
		viewJson = '';
		viewError = '';
		viewLoading = true;
		viewOpen = true;
		try {
			const detail = await api.getVersion(workflowId, version);
			viewJson = JSON.stringify(detail.definition, null, 2);
		} catch (e) {
			viewError = e instanceof Error ? e.message : String(e);
		} finally {
			viewLoading = false;
		}
	}

	function askRestore(version: number) {
		pendingRestore = version;
		confirmOpen = true;
	}

	async function doRestore() {
		const version = pendingRestore;
		if (version == null) return;
		restoring = true;
		try {
			const detail = await api.getVersion(workflowId, version);
			await onrestore(detail.definition, version);
			await load(); // reflect the freshly created version
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			restoring = false;
			pendingRestore = null;
		}
	}
</script>

<Sheet.Root bind:open>
	<Sheet.Content side="right" class="w-full gap-0 p-0 sm:max-w-md">
		<Sheet.Header class="border-b px-4 py-3">
			<Sheet.Title class="flex items-center gap-2">
				<GitCommitVerticalIcon class="text-primary-strong size-4" />
				Version history
			</Sheet.Title>
			<Sheet.Description>
				Every save is an immutable version. Restore one to bring its canvas back as a new version.
			</Sheet.Description>
		</Sheet.Header>

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if loading}
				<p class="text-muted-foreground p-4 text-sm">Loading…</p>
			{:else if error}
				<div class="border-destructive/30 bg-destructive/10 text-destructive m-4 rounded-md border px-3 py-2 text-sm">
					{error}
				</div>
			{:else if versions.length === 0}
				<p class="text-muted-foreground p-4 text-sm">No versions yet — save the canvas to create one.</p>
			{:else}
				<div class="divide-border divide-y">
					{#each versions as v (v.version)}
						<div class="flex items-center gap-3 px-4 py-3">
							<div class="min-w-0 flex-1">
								<div class="flex items-center gap-2">
									<span class="font-medium">v{v.version}</span>
									{#if v.is_current}
										<Badge variant="secondary">current</Badge>
									{/if}
								</div>
								<div class="text-muted-foreground mt-0.5 text-xs">
									{fmtRelative(v.created_at)} · {v.step_count}
									{v.step_count === 1 ? 'step' : 'steps'}
								</div>
							</div>
							<div class="flex shrink-0 items-center gap-1">
								<Button
									variant="ghost"
									size="icon"
									aria-label="View v{v.version}"
									title="View definition"
									onclick={() => view(v.version)}
								>
									<EyeIcon class="size-4" />
								</Button>
								{#if !v.is_current}
									<Button
										variant="outline"
										size="sm"
										onclick={() => askRestore(v.version)}
										disabled={restoring}
									>
										<Undo2Icon class="size-4" /> Restore
									</Button>
								{/if}
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>

<Dialog.Root bind:open={viewOpen}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>Version {viewVersion} definition</Dialog.Title>
			<Dialog.Description>Read-only snapshot of the saved workflow JSON.</Dialog.Description>
		</Dialog.Header>
		{#if viewLoading}
			<p class="text-muted-foreground text-sm">Loading…</p>
		{:else if viewError}
			<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm">
				{viewError}
			</div>
		{:else}
			<pre class="bg-muted max-h-[60vh] overflow-auto rounded-md p-3 font-mono text-xs leading-relaxed">{viewJson}</pre>
		{/if}
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (viewOpen = false)}>Close</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Restore v{pendingRestore}?"
	description={dirty
		? `This discards your unsaved changes and saves v${pendingRestore}'s canvas as a new version.`
		: `This saves v${pendingRestore}'s canvas as a new version. Later versions stay in history.`}
	confirmText="Restore"
	destructive={false}
	onconfirm={doRestore}
/>
