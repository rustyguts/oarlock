<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, mcpUrl, type ApiToken } from '$lib/api';
	import { fmtRelative } from '$lib/flow';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PlugIcon from '@lucide/svelte/icons/plug';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import CheckIcon from '@lucide/svelte/icons/check';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	const endpoint = mcpUrl();

	let tokens = $state<ApiToken[]>([]);
	let error = $state('');
	let loading = $state(true);

	// Create dialog
	let open = $state(false);
	let name = $state('');
	let saving = $state(false);
	let dialogError = $state('');
	// The raw token is returned exactly once — held only until the reveal is dismissed.
	let revealed = $state<{ name: string; token: string } | null>(null);

	let confirmOpen = $state(false);
	let pendingDelete = $state<ApiToken | null>(null);

	// Copy-to-clipboard feedback, keyed by the copied value.
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

	async function refresh() {
		try {
			tokens = await api.listApiTokens();
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}
	onMount(refresh);

	function openCreate() {
		name = '';
		dialogError = '';
		revealed = null;
		open = true;
	}

	async function create(e?: SubmitEvent) {
		e?.preventDefault();
		if (saving || !name.trim()) return;
		saving = true;
		dialogError = '';
		try {
			const res = await api.createApiToken(name.trim());
			revealed = { name: name.trim(), token: res.token };
			await refresh();
		} catch (e) {
			dialogError = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	// Closing the dialog clears the once-shown token so it can never be re-read.
	function closeDialog() {
		open = false;
		revealed = null;
		name = '';
		dialogError = '';
	}

	function remove(t: ApiToken) {
		pendingDelete = t;
		confirmOpen = true;
	}

	async function confirmDelete() {
		const t = pendingDelete;
		if (!t) return;
		try {
			await api.deleteApiToken(t.id);
			error = '';
			await refresh();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}
</script>

<div class="w-full px-6 py-6">
	<div class="mb-6 flex items-center justify-between gap-4">
		<div>
			<h1 class="text-xl font-semibold">MCP Access</h1>
			<p class="text-muted-foreground text-sm">
				Let an AI agent drive this workspace over the Model Context Protocol.
			</p>
		</div>
		<Button onclick={openCreate}><PlusIcon class="size-4" /> Create token</Button>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	<!-- Explainer + endpoint (border, not a filled surface) -->
	<Card.Root class="mb-6 py-5">
		<Card.Content class="space-y-4 px-5">
			<div class="flex items-start gap-3">
				<span class="bg-primary/12 text-primary-strong flex size-9 shrink-0 items-center justify-center rounded-lg">
					<PlugIcon class="size-4.5" />
				</span>
				<div class="min-w-0">
					<div class="font-medium">Connect an AI agent to this workspace</div>
					<p class="text-muted-foreground mt-1 text-sm leading-relaxed">
						Point an MCP client at the URL below and authenticate with a token. Your workflows become
						callable tools:
						<span class="text-foreground font-mono text-xs">list_workflows</span>,
						<span class="text-foreground font-mono text-xs">run_workflow</span>, and
						<span class="text-foreground font-mono text-xs">get_run_status</span>.
					</p>
				</div>
			</div>
			<div>
				<Label class="text-muted-foreground text-xs font-medium">MCP endpoint URL</Label>
				<div class="bg-muted mt-1.5 flex items-center gap-2 rounded-md px-3 py-2">
					<code class="text-foreground min-w-0 flex-1 truncate font-mono text-sm">{endpoint}</code>
					<Button
						variant="ghost"
						size="icon"
						class="size-7 shrink-0"
						onclick={() => copy(endpoint)}
						aria-label="Copy MCP URL"
					>
						{#if copied === endpoint}
							<CheckIcon class="size-4 text-emerald-500" />
						{:else}
							<CopyIcon class="size-4" />
						{/if}
					</Button>
				</div>
			</div>
		</Card.Content>
	</Card.Root>

	<h2 class="mb-3 text-sm font-semibold">Tokens</h2>

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if tokens.length === 0}
		<Card.Root class="border-dashed">
			<Card.Content class="py-12 text-center">
				<KeyRoundIcon class="text-muted-foreground/50 mx-auto size-8" />
				<p class="text-muted-foreground mt-3">No tokens yet.</p>
				<p class="text-muted-foreground/70 mt-1 text-sm">
					Create one to authenticate an MCP client against this workspace.
				</p>
			</Card.Content>
		</Card.Root>
	{:else}
		<div class="space-y-3">
			{#each tokens as t (t.id)}
				<Card.Root class="py-4">
					<Card.Content class="flex items-center gap-4 px-4">
						<div class="bg-muted text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-lg">
							<KeyRoundIcon class="size-5" />
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2">
								<span class="font-medium">{t.name}</span>
								<code class="text-muted-foreground font-mono text-xs">{t.prefix}…</code>
							</div>
							<div class="text-muted-foreground mt-0.5 flex items-center gap-2 text-xs">
								<span>created {fmtRelative(t.created_at)}</span>
								<span>·</span>
								<span>
									{t.last_used_at ? `last used ${fmtRelative(t.last_used_at)}` : 'never used'}
								</span>
							</div>
						</div>
						<Button
							variant="ghost"
							size="icon"
							class="text-muted-foreground hover:text-destructive shrink-0"
							onclick={() => remove(t)}
							aria-label="Delete token"
						>
							<Trash2Icon class="size-4" />
						</Button>
					</Card.Content>
				</Card.Root>
			{/each}
		</div>
	{/if}
</div>

<Dialog.Root bind:open onOpenChange={(v) => (v ? null : closeDialog())}>
	<Dialog.Content class="sm:max-w-md">
		{#if revealed}
			<Dialog.Header>
				<Dialog.Title>Token created</Dialog.Title>
				<Dialog.Description>
					Copy “{revealed.name}” now — you won’t be able to see it again.
				</Dialog.Description>
			</Dialog.Header>
			<div class="space-y-4">
				<div class="rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/60">
					<div class="flex items-center gap-1.5 text-xs font-medium text-amber-700 dark:text-amber-300">
						<TriangleAlertIcon class="size-3.5" /> Shown once — store it somewhere safe.
					</div>
					<div class="bg-background/80 mt-2 flex items-center gap-2 rounded border px-2.5 py-1.5">
						<code class="text-foreground min-w-0 flex-1 break-all font-mono text-sm">{revealed.token}</code>
						<Button
							variant="ghost"
							size="icon"
							class="size-7 shrink-0"
							onclick={() => copy(revealed!.token)}
							aria-label="Copy token"
						>
							{#if copied === revealed.token}
								<CheckIcon class="size-4 text-emerald-500" />
							{:else}
								<CopyIcon class="size-4" />
							{/if}
						</Button>
					</div>
				</div>
				<p class="text-muted-foreground text-xs">
					Use it as a bearer token when connecting an MCP client to
					<code class="text-foreground font-mono">{endpoint}</code>.
				</p>
			</div>
			<Dialog.Footer>
				<Button onclick={closeDialog}>Done</Button>
			</Dialog.Footer>
		{:else}
			<form class="contents" onsubmit={create}>
				<Dialog.Header>
					<Dialog.Title>Create token</Dialog.Title>
					<Dialog.Description>
						A bearer token that lets an MCP client act as this workspace.
					</Dialog.Description>
				</Dialog.Header>
				<div class="space-y-4">
					<div class="space-y-1.5">
						<Label for="token-name">Name</Label>
						<Input id="token-name" bind:value={name} placeholder="claude-desktop" autocomplete="off" />
						<p class="text-muted-foreground text-xs">A label to recognise this token later.</p>
					</div>
					{#if dialogError}
						<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
							{dialogError}
						</div>
					{/if}
				</div>
				<Dialog.Footer>
					<Button type="submit" disabled={saving || !name.trim()}>
						{saving ? 'Creating…' : 'Create token'}
					</Button>
				</Dialog.Footer>
			</form>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Delete token?"
	description={pendingDelete
		? `"${pendingDelete.name}" will stop working immediately for any MCP client using it.`
		: ''}
	confirmText="Delete"
	onconfirm={confirmDelete}
/>
