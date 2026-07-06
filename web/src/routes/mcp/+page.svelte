<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, type MCPServer, type MCPToolInfo } from '$lib/api';
	import { fmtRelative } from '$lib/flow';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import ServerIcon from '@lucide/svelte/icons/server';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import LockIcon from '@lucide/svelte/icons/lock';
	import PlugZapIcon from '@lucide/svelte/icons/plug-zap';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import LoaderIcon from '@lucide/svelte/icons/loader';

	let servers = $state<MCPServer[]>([]);
	let error = $state('');
	let loading = $state(true);

	// dialog state
	let open = $state(false);
	let editing = $state<MCPServer | null>(null);
	let name = $state('');
	let url = $state('');
	let authHeader = $state('');
	let enabled = $state(true);
	let saving = $state(false);
	let dialogError = $state('');
	let testing = $state(false);
	let testResult = $state<MCPToolInfo[] | null>(null);
	let testError = $state('');

	let confirmOpen = $state(false);
	let pendingDelete = $state<MCPServer | null>(null);

	// per-server discovered tool counts (lazy, after list load)
	let toolCounts = $state<Record<string, number | 'error'>>({});

	async function refresh() {
		try {
			servers = await api.listMCPServers();
			error = '';
			for (const srv of servers.filter((x) => x.is_enabled)) {
				api
					.mcpServerTools(srv.id)
					.then((tools) => (toolCounts = { ...toolCounts, [srv.id]: tools.length }))
					.catch(() => (toolCounts = { ...toolCounts, [srv.id]: 'error' }));
			}
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}
	onMount(refresh);

	function openCreate() {
		editing = null;
		name = '';
		url = '';
		authHeader = '';
		enabled = true;
		dialogError = '';
		testResult = null;
		testError = '';
		open = true;
	}

	function openEdit(srv: MCPServer) {
		editing = srv;
		name = srv.name;
		url = srv.url;
		authHeader = ''; // never echoed back; blank = keep existing
		enabled = srv.is_enabled;
		dialogError = '';
		testResult = null;
		testError = '';
		open = true;
	}

	async function save(e?: SubmitEvent) {
		e?.preventDefault();
		if (saving || !name.trim() || !url.trim()) return;
		saving = true;
		dialogError = '';
		try {
			if (editing) {
				await api.updateMCPServer(editing.id, {
					name: name.trim(),
					url: url.trim(),
					is_enabled: enabled,
					...(authHeader.trim() !== '' ? { auth_header: authHeader.trim() } : {})
				});
			} else {
				await api.createMCPServer({
					name: name.trim(),
					url: url.trim(),
					...(authHeader.trim() !== '' ? { auth_header: authHeader.trim() } : {})
				});
			}
			open = false;
			await refresh();
		} catch (e) {
			dialogError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	// Test connection statelessly — probe the endpoint without persisting a
	// server. When editing an authed server without re-entering the header, fall
	// back to the stored server's live tool list (the plaintext auth is gone).
	async function testConnection() {
		testing = true;
		testError = '';
		testResult = null;
		try {
			if (editing && authHeader.trim() === '') {
				testResult = await api.mcpServerTools(editing.id);
			} else {
				testResult = await api.mcpTest({
					url: url.trim(),
					...(authHeader.trim() !== '' ? { auth_header: authHeader.trim() } : {})
				});
			}
		} catch (e) {
			testError = e instanceof Error ? e.message : String(e);
		} finally {
			testing = false;
		}
	}

	function remove(srv: MCPServer) {
		pendingDelete = srv;
		confirmOpen = true;
	}

	async function confirmDelete() {
		const srv = pendingDelete;
		if (!srv) return;
		try {
			await api.deleteMCPServer(srv.id);
			error = '';
			await refresh();
		} catch (e) {
			if (e instanceof ApiError && e.workflows?.length) {
				error = `${e.message}: ${e.workflows.join(', ')} — remove the step(s) first.`;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		}
	}
</script>

<div class="w-full px-6 py-6">
	<div class="mb-6 flex items-center justify-between gap-4">
		<div>
			<h1 class="text-xl font-semibold">MCP Servers</h1>
			<p class="text-muted-foreground text-sm">
				Model Context Protocol servers this workspace can call from workflows.
			</p>
		</div>
		<Button onclick={openCreate}><PlusIcon class="size-4" /> Add server</Button>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if servers.length === 0}
		<Card.Root class="border-dashed">
			<Card.Content class="py-12 text-center">
				<ServerIcon class="text-muted-foreground/50 mx-auto size-8" />
				<p class="text-muted-foreground mt-3">No MCP servers yet.</p>
				<p class="text-muted-foreground/70 mt-1 text-sm">
					Add one to call its tools from the <span class="font-mono text-xs">mcp.tool</span> step.
				</p>
			</Card.Content>
		</Card.Root>
	{:else}
		<div class="space-y-3">
			{#each servers as srv (srv.id)}
				<Card.Root class="py-4">
					<Card.Content class="flex items-center gap-4 px-4">
						<div
							class="flex size-10 shrink-0 items-center justify-center rounded-lg
							{srv.is_enabled ? 'bg-primary/15 text-primary-strong' : 'bg-muted text-muted-foreground'}"
						>
							<ServerIcon class="size-5" />
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2">
								<span class="font-medium">{srv.name}</span>
								{#if !srv.is_enabled}
									<Badge variant="secondary">disabled</Badge>
								{/if}
								{#if srv.has_auth}
									<LockIcon class="text-muted-foreground size-3.5" aria-label="Authenticated" />
								{/if}
							</div>
							<div class="text-muted-foreground mt-0.5 flex items-center gap-2 truncate text-xs">
								<span class="truncate font-mono">{srv.url}</span>
								<span>·</span>
								<span>updated {fmtRelative(srv.updated_at)}</span>
							</div>
						</div>
						<div class="flex shrink-0 items-center gap-1.5">
							{#if srv.is_enabled}
								{#if toolCounts[srv.id] === undefined}
									<Badge variant="outline" class="text-muted-foreground gap-1">
										<LoaderIcon class="size-3 animate-spin" /> tools
									</Badge>
								{:else if toolCounts[srv.id] === 'error'}
									<Badge variant="outline" class="text-destructive">unreachable</Badge>
								{:else}
									<Badge variant="outline" class="gap-1">
										<WrenchIcon class="size-3" />
										{toolCounts[srv.id]} tools
									</Badge>
								{/if}
							{/if}
							<Button variant="ghost" size="icon" onclick={() => openEdit(srv)} aria-label="Edit">
								<PencilIcon class="size-4" />
							</Button>
							<Button
								variant="ghost"
								size="icon"
								class="text-muted-foreground hover:text-destructive"
								onclick={() => remove(srv)}
								aria-label="Delete"
							>
								<Trash2Icon class="size-4" />
							</Button>
						</div>
					</Card.Content>
				</Card.Root>
			{/each}
		</div>
	{/if}
</div>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-md">
		<form class="contents" onsubmit={save}>
			<Dialog.Header>
				<Dialog.Title>{editing ? 'Edit MCP server' : 'Add MCP server'}</Dialog.Title>
				<Dialog.Description>
					Streamable-HTTP MCP endpoint. Tools become available to workflow steps.
				</Dialog.Description>
			</Dialog.Header>

			<div class="space-y-4">
			<div class="space-y-1.5">
				<Label for="mcp-name">Name</Label>
				<Input id="mcp-name" bind:value={name} placeholder="github-tools" />
				<p class="text-muted-foreground text-xs">Referenced by workflows — renaming is blocked while in use.</p>
			</div>
			<div class="space-y-1.5">
				<Label for="mcp-url">Endpoint URL</Label>
				<Input id="mcp-url" bind:value={url} placeholder="https://mcp.example.com/mcp" class="font-mono text-sm" />
			</div>
			<div class="space-y-1.5">
				<Label for="mcp-auth">Authorization header <span class="text-muted-foreground">(optional)</span></Label>
				<Input
					id="mcp-auth"
					bind:value={authHeader}
					type="password"
					placeholder={editing?.has_auth ? '•••••• (leave blank to keep)' : 'Bearer sk-…'}
					class="font-mono text-sm"
				/>
				<p class="text-muted-foreground text-xs">Encrypted at rest; never shown again.</p>
			</div>
			{#if editing}
				<div class="flex items-center justify-between">
					<Label for="mcp-enabled">Enabled</Label>
					<Switch id="mcp-enabled" bind:checked={enabled} />
				</div>
			{/if}

			{#if testResult}
				<div class="rounded-md border border-emerald-200 bg-emerald-50 p-3 dark:border-emerald-900 dark:bg-emerald-950">
					<p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">
						Connected — {testResult.length} {testResult.length === 1 ? 'tool' : 'tools'}
					</p>
					<ul class="mt-1.5 space-y-1">
						{#each testResult as tool (tool.name)}
							<li class="text-xs text-emerald-700/80 dark:text-emerald-300/80">
								<span class="font-mono font-medium">{tool.name}</span>
								{#if tool.description}— {tool.description}{/if}
							</li>
						{/each}
					</ul>
				</div>
			{/if}
			{#if testError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{testError}
				</div>
			{/if}
			{#if dialogError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{dialogError}
				</div>
			{/if}
		</div>

			<Dialog.Footer class="gap-2">
				<Button type="button" variant="outline" onclick={testConnection} disabled={testing || !url.trim()}>
					{#if testing}
						<LoaderIcon class="size-4 animate-spin" /> Testing…
					{:else}
						<PlugZapIcon class="size-4" /> Test connection
					{/if}
				</Button>
				<Button type="submit" disabled={saving || !name.trim() || !url.trim()}>
					{saving ? 'Saving…' : editing ? 'Save changes' : 'Add server'}
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Remove MCP server?"
	description={pendingDelete
		? `"${pendingDelete.name}" will be removed from this workspace.`
		: ''}
	confirmText="Remove"
	onconfirm={confirmDelete}
/>
