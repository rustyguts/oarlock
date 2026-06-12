<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, type Secret } from '$lib/api';
	import { fmtRelative } from '$lib/flow';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';

	const PROVIDERS = [
		{ value: 'anthropic', label: 'Anthropic', hint: 'sk-ant-…' },
		{ value: 'openai', label: 'OpenAI', hint: 'sk-…' },
		{ value: 'openrouter', label: 'OpenRouter', hint: 'sk-or-…' }
	];
	const TYPES = [
		{
			value: 'generic',
			label: 'Generic',
			description: 'Any sensitive value, usable in every step via {{secrets.<name>}}'
		},
		{ value: 'api_key', label: 'API Key', description: 'Bring-your-own LLM key for AI steps' }
	];

	let secrets = $state<Secret[]>([]);
	let error = $state('');
	let loading = $state(true);

	let open = $state(false);
	let name = $state('');
	let type = $state('generic');
	let provider = $state('anthropic');
	let value = $state('');
	let saving = $state(false);
	let dialogError = $state('');

	let typeLabel = $derived(TYPES.find((t) => t.value === type)?.label ?? type);
	let providerLabel = $derived(PROVIDERS.find((p) => p.value === provider)?.label ?? provider);

	async function refresh() {
		try {
			secrets = await api.listSecrets();
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
		type = 'generic';
		provider = 'anthropic';
		value = '';
		dialogError = '';
		open = true;
	}

	async function save() {
		saving = true;
		dialogError = '';
		try {
			await api.createSecret({
				name: name.trim(),
				type,
				...(type === 'api_key' ? { provider } : {}),
				value: value.trim()
			});
			open = false;
			await refresh();
		} catch (e) {
			dialogError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	async function remove(s: Secret) {
		if (!confirm(`Remove secret "${s.name}"? Workflows using it will stop working.`)) return;
		try {
			await api.deleteSecret(s.id);
			error = '';
			await refresh();
		} catch (e) {
			if (e instanceof ApiError && e.workflows?.length) {
				error = `${e.message}: ${e.workflows.join(', ')} — remove the reference(s) first.`;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		}
	}
</script>

<div class="w-full px-6 py-6">
	<div class="mb-6">
		<h1 class="text-xl font-semibold">Configuration</h1>
		<p class="text-muted-foreground text-sm">Workspace settings and secrets.</p>
	</div>

	<div class="mb-3 flex items-center justify-between gap-4">
		<div>
			<h2 class="font-medium">Secrets</h2>
			<p class="text-muted-foreground text-xs">
				Encrypted at rest, write-only, redacted from run records and logs. Reference anywhere with
				<code class="bg-muted rounded px-1">{'{{secrets.<name>}}'}</code>.
			</p>
		</div>
		<Button onclick={openCreate}><PlusIcon class="size-4" /> Add secret</Button>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if secrets.length === 0}
		<Card.Root class="border-dashed">
			<Card.Content class="py-12 text-center">
				<ShieldIcon class="text-muted-foreground/50 mx-auto size-8" />
				<p class="text-muted-foreground mt-3">No secrets yet.</p>
				<p class="text-muted-foreground/70 mt-1 text-sm">
					Store API keys for AI steps, or any sensitive value your workflows need.
				</p>
			</Card.Content>
		</Card.Root>
	{:else}
		<Card.Root class="py-0">
			<Card.Content class="divide-border divide-y px-0">
				{#each secrets as s (s.id)}
					<div class="flex items-center gap-4 px-4 py-3">
						<div class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
							{#if s.type === 'api_key'}
								<KeyRoundIcon class="size-4" />
							{:else}
								<ShieldIcon class="size-4" />
							{/if}
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2">
								<span class="font-mono text-sm font-medium">{s.name}</span>
								<Badge variant="secondary">{s.type === 'api_key' ? 'API Key' : 'Generic'}</Badge>
								{#if s.provider}
									<Badge variant="outline" class="capitalize">{s.provider}</Badge>
								{/if}
							</div>
							<div class="text-muted-foreground mt-0.5 text-xs">
								<span class="font-mono">{s.value_hint || '••••'}</span>
								· added {fmtRelative(s.created_at)}
							</div>
						</div>
						<Button
							variant="ghost"
							size="icon"
							class="text-muted-foreground hover:text-destructive shrink-0"
							onclick={() => remove(s)}
							aria-label="Delete"
						>
							<Trash2Icon class="size-4" />
						</Button>
					</div>
				{/each}
			</Card.Content>
		</Card.Root>
	{/if}
</div>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Add secret</Dialog.Title>
			<Dialog.Description>
				Encrypted in the database and never shown again. Values are redacted from all run output
				and logs.
			</Dialog.Description>
		</Dialog.Header>
		<div class="space-y-4">
			<div class="space-y-1.5">
				<Label>Type</Label>
				<Select.Root type="single" bind:value={type}>
					<Select.Trigger class="w-full">{typeLabel}</Select.Trigger>
					<Select.Content>
						{#each TYPES as t (t.value)}
							<Select.Item value={t.value}>{t.label}</Select.Item>
						{/each}
					</Select.Content>
				</Select.Root>
				<p class="text-muted-foreground text-xs">{TYPES.find((t) => t.value === type)?.description}</p>
			</div>
			{#if type === 'api_key'}
				<div class="space-y-1.5">
					<Label>Provider</Label>
					<Select.Root type="single" bind:value={provider}>
						<Select.Trigger class="w-full">{providerLabel}</Select.Trigger>
						<Select.Content>
							{#each PROVIDERS as p (p.value)}
								<Select.Item value={p.value}>{p.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
			{/if}
			<div class="space-y-1.5">
				<Label for="secret-name">Name</Label>
				<Input
					id="secret-name"
					bind:value={name}
					placeholder={type === 'api_key' ? `my_${provider}` : 'webhook_token'}
					class="font-mono text-sm"
				/>
				<p class="text-muted-foreground text-xs">
					Letters, numbers, _ and - only — referenced as
					<code class="bg-muted rounded px-1">{'secrets.<name>'}</code>.
				</p>
			</div>
			<div class="space-y-1.5">
				<Label for="secret-value">Value</Label>
				<Input
					id="secret-value"
					bind:value
					type="password"
					placeholder={type === 'api_key' ? PROVIDERS.find((p) => p.value === provider)?.hint : '••••••••'}
					class="font-mono text-sm"
				/>
			</div>
			{#if dialogError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{dialogError}
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button onclick={save} disabled={saving || !name.trim() || !value.trim()}>
				{saving ? 'Saving…' : 'Add secret'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
