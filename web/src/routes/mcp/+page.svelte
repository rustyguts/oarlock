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

	async function save() {
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

	// Test connection: save-less for edits isn't possible (the server holds the
	// secret), so for new entries we create→test→keep, and surface tools live.
	async function testConnection() {
		testing = true;
		testError = '';
		testResult = null;
		try {
			if (editing) {
				testResult = await api.mcpServerTools(editing.id);
			} else {
				const { id } = await api.createMCPServer({
					name: name.trim() || 'untitled',
					url: url.trim(),
					...(authHeader.trim() !== '' ? { auth_header: authHeader.trim() } : {})
				});
				try {
					testResult = await api.mcpServerTools(id);
				} finally {
					await api.deleteMCPServer(id).catch(() => {});
				}
			}
		} catch (e) {
			testError = e instanceof Error ? e.message : String(e);
		} finally {
			testing = false;
		}
	}

	async function remove(srv: MCPServer) {
		if (!confirm(`Remove connection "${srv.name}"?`)) return;
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
	<div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
		<div>
			<h1 class="text-xl font-semibold">Connections</h1>
			<p class="text-muted-foreground text-sm">
				External tool connections (MCP servers) this workspace can call from workflows.
			</p>
		</div>
		<Button onclick={openCreate}><PlusIcon class="size-4" /> Add connection</Button>
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
				<p class="text-muted-foreground mt-3">No connections yet.</p>
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
							{srv.is_enabled ? 'bg-primary/15 text-primary-foreground dark:text-primary' : 'bg-muted text-muted-foreground'}"
						>
							<ServerIcon class="size-5" />
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex min-w-0 items-center gap-2">
								<span class="truncate font-medium">{srv.name}</span>
								{#if !srv.is_enabled}
									<Badge variant="secondary" class="shrink-0">disabled</Badge>
								{/if}
								{#if srv.has_auth}
									<LockIcon class="text-muted-foreground size-3.5 shrink-0" aria-label="Authenticated" />
								{/if}
							</div>
							<div class="text-muted-foreground mt-0.5 flex min-w-0 items-center gap-1.5 text-xs">
								<span class="truncate font-mono">{srv.url}</span>
								<span class="shrink-0">·</span>
								<span class="shrink-0 whitespace-nowrap">updated {fmtRelative(srv.updated_at)}</span>
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
		<Dialog.Header>
			<Dialog.Title>{editing ? 'Edit connection' : 'Add connection'}</Dialog.Title>
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
			<Button variant="outline" onclick={testConnection} disabled={testing || !url.trim()}>
				{#if testing}
					<LoaderIcon class="size-4 animate-spin" /> Testing…
				{:else}
					<PlugZapIcon class="size-4" /> Test connection
				{/if}
			</Button>
			<Button onclick={save} disabled={saving || !name.trim() || !url.trim()}>
				{saving ? 'Saving…' : editing ? 'Save changes' : 'Add connection'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
