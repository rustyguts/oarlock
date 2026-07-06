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
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import XIcon from '@lucide/svelte/icons/x';

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
	// Dev-key warning: secrets are encrypted with the built-in key unless
	// OARLOCK_MASTER_KEY is set. Older APIs omit the field — treat as not-dev.
	let devKey = $state(false);
	let bannerDismissed = $state(false);

	let open = $state(false);
	let name = $state('');
	let type = $state('generic');
	let provider = $state('anthropic');
	let value = $state('');
	let saving = $state(false);
	let dialogError = $state('');

	let confirmOpen = $state(false);
	let pendingDelete = $state<Secret | null>(null);

	// Rotate dialog
	let rotateOpen = $state(false);
	let rotateTarget = $state<Secret | null>(null);
	let rotateValue = $state('');
	let rotating = $state(false);
	let rotateError = $state('');

	// Transient success flash
	let notice = $state('');
	let noticeTimer: ReturnType<typeof setTimeout> | null = null;
	function flashOk(msg: string) {
		notice = msg;
		if (noticeTimer) clearTimeout(noticeTimer);
		noticeTimer = setTimeout(() => (notice = ''), 2500);
	}

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
	onMount(() => {
		refresh();
		api
			.me()
			.then((me) => (devKey = me.vault?.dev_key === true))
			.catch(() => {
				/* banner stays hidden if we can't tell */
			});
	});

	function openCreate() {
		name = '';
		type = 'generic';
		provider = 'anthropic';
		value = '';
		dialogError = '';
		open = true;
	}

	async function save(e?: SubmitEvent) {
		e?.preventDefault();
		if (saving || !name.trim() || !value.trim()) return;
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

	function openRotate(s: Secret) {
		rotateTarget = s;
		rotateValue = '';
		rotateError = '';
		rotateOpen = true;
	}

	async function rotate(e?: SubmitEvent) {
		e?.preventDefault();
		const s = rotateTarget;
		if (!s || rotating || !rotateValue.trim()) return;
		rotating = true;
		rotateError = '';
		try {
			await api.rotateSecret(s.id, rotateValue.trim());
			rotateOpen = false;
			await refresh(); // value_hint reflects the new value
			flashOk(`Rotated ${s.name}`);
		} catch (err) {
			rotateError = err instanceof Error ? err.message : String(err);
		} finally {
			rotating = false;
		}
	}

	function remove(s: Secret) {
		pendingDelete = s;
		confirmOpen = true;
	}

	async function confirmDelete() {
		const s = pendingDelete;
		if (!s) return;
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

	{#if devKey && !bannerDismissed}
		<div class="mb-4 flex items-start gap-3 rounded-md border border-amber-500/40 px-3 py-2.5 text-sm">
			<TriangleAlertIcon class="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
			<div class="min-w-0 flex-1">
				<p class="font-medium text-amber-700 dark:text-amber-300">
					Using the built-in development key
				</p>
				<p class="text-muted-foreground mt-0.5">
					Secrets are encrypted with a default key baked into the build. Set
					<code class="bg-muted rounded px-1">OARLOCK_MASTER_KEY</code> to protect them at rest.
				</p>
			</div>
			<button
				type="button"
				onclick={() => (bannerDismissed = true)}
				class="text-muted-foreground hover:text-foreground shrink-0"
				aria-label="Dismiss"
			>
				<XIcon class="size-4" />
			</button>
		</div>
	{/if}

	<div class="mb-3 flex items-center justify-between gap-4">
		<div>
			<h2 class="font-medium">Secrets</h2>
			<p class="text-muted-foreground text-xs">
				Encrypted at rest, write-only, redacted from run records and logs. Reference anywhere with
				<code class="bg-muted rounded px-1">{'{{secrets.<name>}}'}</code>.
			</p>
		</div>
		<div class="flex items-center gap-3">
			{#if notice}
				<span class="text-xs text-emerald-600 dark:text-emerald-400">{notice}</span>
			{/if}
			<Button onclick={openCreate}><PlusIcon class="size-4" /> Add secret</Button>
		</div>
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
						<div class="flex shrink-0 items-center gap-1">
							<Button variant="outline" size="sm" onclick={() => openRotate(s)}>
								<RotateCwIcon class="size-4" /> Rotate…
							</Button>
							<Button
								variant="ghost"
								size="icon"
								class="text-muted-foreground hover:text-destructive"
								onclick={() => remove(s)}
								aria-label="Delete"
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

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-md">
		<form class="contents" onsubmit={save}>
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
				<Button type="submit" disabled={saving || !name.trim() || !value.trim()}>
					{saving ? 'Saving…' : 'Add secret'}
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={rotateOpen}>
	<Dialog.Content class="sm:max-w-md">
		<form class="contents" onsubmit={rotate}>
			<Dialog.Header>
				<Dialog.Title>Rotate secret</Dialog.Title>
				<Dialog.Description>
					Replace the value of
					<code class="bg-muted rounded px-1 font-mono">{rotateTarget?.name}</code>. The name and
					references stay the same; the new value is encrypted and never shown again.
				</Dialog.Description>
			</Dialog.Header>
			<div class="space-y-1.5">
				<Label for="rotate-value">New value</Label>
				<Input
					id="rotate-value"
					bind:value={rotateValue}
					type="password"
					placeholder="••••••••"
					class="font-mono text-sm"
				/>
			</div>
			{#if rotateError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{rotateError}
				</div>
			{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (rotateOpen = false)}>Cancel</Button>
				<Button type="submit" disabled={rotating || !rotateValue.trim()}>
					{rotating ? 'Rotating…' : 'Rotate secret'}
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Remove secret?"
	description={pendingDelete
		? `"${pendingDelete.name}" will be deleted. Workflows referencing it will stop working.`
		: ''}
	confirmText="Remove"
	onconfirm={confirmDelete}
/>
