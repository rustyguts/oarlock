<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, type Secret, type ComputeTarget } from '$lib/api';
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
	import BoxesIcon from '@lucide/svelte/icons/boxes';
	import ContainerIcon from '@lucide/svelte/icons/container';
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
		{ value: 'api_key', label: 'API Key', description: 'Bring-your-own LLM key for AI steps' },
		{
			value: 'registry',
			label: 'Registry',
			description: 'Private container-registry username + token for container.run steps'
		}
	];
	const BACKENDS = [
		{ value: 'docker', label: 'Docker (local)' },
		{ value: 'k8s', label: 'Kubernetes Jobs' }
	];

	let secrets = $state<Secret[]>([]);
	let computeTargets = $state<ComputeTarget[]>([]);
	let error = $state('');
	let loading = $state(true);

	// secret dialog
	let open = $state(false);
	let name = $state('');
	let type = $state('generic');
	let provider = $state('anthropic');
	let value = $state('');
	let username = $state('');
	let password = $state('');
	let saving = $state(false);
	let dialogError = $state('');

	let typeLabel = $derived(TYPES.find((t) => t.value === type)?.label ?? type);
	let providerLabel = $derived(PROVIDERS.find((p) => p.value === provider)?.label ?? provider);
	let registrySecrets = $derived(secrets.filter((s) => s.type === 'registry'));

	// compute-target dialog
	let ctOpen = $state(false);
	let ctName = $state('');
	let ctBackend = $state('docker');
	let ctCpu = $state('1');
	let ctMem = $state(1024);
	let ctTimeout = $state(600);
	let ctAllowlist = $state('');
	let ctRegistry = $state('');
	let ctNamespace = $state('');
	let ctRuntimeClass = $state('');
	let ctSaving = $state(false);
	let ctError = $state('');
	let ctBackendLabel = $derived(BACKENDS.find((b) => b.value === ctBackend)?.label ?? ctBackend);

	async function refresh() {
		try {
			[secrets, computeTargets] = await Promise.all([
				api.listSecrets(),
				api.listComputeTargets().catch(() => [])
			]);
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
		username = '';
		password = '';
		dialogError = '';
		open = true;
	}

	let secretValid = $derived(
		!!name.trim() && (type === 'registry' ? !!password.trim() : !!value.trim())
	);

	async function save() {
		saving = true;
		dialogError = '';
		try {
			await api.createSecret({
				name: name.trim(),
				type,
				...(type === 'api_key' ? { provider } : {}),
				...(type === 'registry'
					? { username: username.trim(), password: password.trim() }
					: { value: value.trim() })
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

	function openCreateCT() {
		ctName = '';
		ctBackend = 'docker';
		ctCpu = '1';
		ctMem = 1024;
		ctTimeout = 600;
		ctAllowlist = '';
		ctRegistry = '';
		ctNamespace = '';
		ctRuntimeClass = '';
		ctError = '';
		ctOpen = true;
	}

	async function saveCT() {
		ctSaving = true;
		ctError = '';
		try {
			await api.createComputeTarget({
				name: ctName.trim(),
				backend: ctBackend,
				cpu_limit: ctCpu.trim() || '1',
				memory_mb_limit: Number(ctMem) || 1024,
				timeout_sec_limit: Number(ctTimeout) || 600,
				image_allowlist: ctAllowlist
					.split(/[\n,]/)
					.map((s) => s.trim())
					.filter(Boolean),
				...(ctRegistry ? { registry_secret_name: ctRegistry } : {}),
				...(ctBackend === 'k8s'
					? { namespace: ctNamespace.trim(), runtime_class: ctRuntimeClass.trim() }
					: {})
			});
			ctOpen = false;
			await refresh();
		} catch (e) {
			ctError = e instanceof Error ? e.message : String(e);
		} finally {
			ctSaving = false;
		}
	}

	async function removeCT(t: ComputeTarget) {
		if (!confirm(`Remove compute target "${t.name}"?`)) return;
		try {
			await api.deleteComputeTarget(t.id);
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
		<p class="text-muted-foreground text-sm">Workspace settings, secrets, and compute.</p>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	<!-- Secrets -->
	<div class="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
		<div>
			<h2 class="font-medium">Secrets</h2>
			<p class="text-muted-foreground text-xs">
				Encrypted at rest, write-only, redacted from run records and logs. Reference anywhere with
				<code class="bg-muted rounded px-1">{'{{secrets.<name>}}'}</code>.
			</p>
		</div>
		<Button onclick={openCreate}><PlusIcon class="size-4" /> Add secret</Button>
	</div>

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if secrets.length === 0}
		<Card.Root class="border-dashed">
			<Card.Content class="py-12 text-center">
				<ShieldIcon class="text-muted-foreground/50 mx-auto size-8" />
				<p class="text-muted-foreground mt-3">No secrets yet.</p>
				<p class="text-muted-foreground/70 mt-1 text-sm">
					Store API keys for AI steps, registry credentials, or any sensitive value.
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
							{:else if s.type === 'registry'}
								<ContainerIcon class="size-4" />
							{:else}
								<ShieldIcon class="size-4" />
							{/if}
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
								<span class="font-mono text-sm font-medium">{s.name}</span>
								<Badge variant="secondary">
									{s.type === 'api_key' ? 'API Key' : s.type === 'registry' ? 'Registry' : 'Generic'}
								</Badge>
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

	<!-- Compute targets -->
	<div class="mt-10 mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
		<div>
			<h2 class="font-medium">Compute targets</h2>
			<p class="text-muted-foreground text-xs">
				Where <code class="bg-muted rounded px-1">container.run</code> steps execute — backend, resource
				ceilings, and image allowlist.
			</p>
		</div>
		<Button onclick={openCreateCT} variant="outline"><PlusIcon class="size-4" /> Add target</Button>
	</div>

	{#if !loading}
		{#if computeTargets.length === 0}
			<Card.Root class="border-dashed">
				<Card.Content class="py-12 text-center">
					<BoxesIcon class="text-muted-foreground/50 mx-auto size-8" />
					<p class="text-muted-foreground mt-3">No compute targets yet.</p>
					<p class="text-muted-foreground/70 mt-1 text-sm">
						Add one to run container steps. Use Docker locally; Kubernetes Jobs at scale.
					</p>
				</Card.Content>
			</Card.Root>
		{:else}
			<Card.Root class="py-0">
				<Card.Content class="divide-border divide-y px-0">
					{#each computeTargets as t (t.id)}
						<div class="flex items-center gap-4 px-4 py-3">
							<div class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
								<BoxesIcon class="size-4" />
							</div>
							<div class="min-w-0 flex-1">
								<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
									<span class="font-mono text-sm font-medium">{t.name}</span>
									<Badge variant="secondary">{t.backend}</Badge>
									{#if t.runtime_class}
										<Badge variant="outline">{t.runtime_class}</Badge>
									{/if}
									{#if !t.is_enabled}
										<Badge variant="outline" class="text-muted-foreground">disabled</Badge>
									{/if}
								</div>
								<div class="text-muted-foreground mt-0.5 text-xs">
									≤ {t.cpu_limit} CPU · ≤ {t.memory_mb_limit} MB · ≤ {t.timeout_sec_limit}s{#if t.image_allowlist.length}
										· {t.image_allowlist.length} allowed image(s){/if}
								</div>
							</div>
							<Button
								variant="ghost"
								size="icon"
								class="text-muted-foreground hover:text-destructive shrink-0"
								onclick={() => removeCT(t)}
								aria-label="Delete"
							>
								<Trash2Icon class="size-4" />
							</Button>
						</div>
					{/each}
				</Card.Content>
			</Card.Root>
		{/if}
	{/if}
</div>

<!-- Add secret dialog -->
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
					placeholder={type === 'api_key' ? `my_${provider}` : type === 'registry' ? 'ghcr' : 'webhook_token'}
					class="font-mono text-sm"
				/>
				<p class="text-muted-foreground text-xs">
					Letters, numbers, _ and - only — referenced as
					<code class="bg-muted rounded px-1">{'secrets.<name>'}</code>.
				</p>
			</div>
			{#if type === 'registry'}
				<div class="space-y-1.5">
					<Label for="reg-user">Username</Label>
					<Input id="reg-user" bind:value={username} placeholder="username or _json_key" class="font-mono text-sm" />
				</div>
				<div class="space-y-1.5">
					<Label for="reg-pass">Password / token</Label>
					<Input id="reg-pass" bind:value={password} type="password" placeholder="••••••••" class="font-mono text-sm" />
				</div>
			{:else}
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
			{/if}
			{#if dialogError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{dialogError}
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button onclick={save} disabled={saving || !secretValid}>
				{saving ? 'Saving…' : 'Add secret'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<!-- Add compute target dialog -->
<Dialog.Root bind:open={ctOpen}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Add compute target</Dialog.Title>
			<Dialog.Description>
				A profile for where and how container steps run. Step resource requests are clamped to these
				ceilings.
			</Dialog.Description>
		</Dialog.Header>
		<div class="space-y-4">
			<div class="space-y-1.5">
				<Label for="ct-name">Name</Label>
				<Input id="ct-name" bind:value={ctName} placeholder="local" class="font-mono text-sm" />
			</div>
			<div class="space-y-1.5">
				<Label>Backend</Label>
				<Select.Root type="single" bind:value={ctBackend}>
					<Select.Trigger class="w-full">{ctBackendLabel}</Select.Trigger>
					<Select.Content>
						{#each BACKENDS as b (b.value)}
							<Select.Item value={b.value}>{b.label}</Select.Item>
						{/each}
					</Select.Content>
				</Select.Root>
			</div>
			<div class="grid grid-cols-3 gap-2">
				<div class="space-y-1.5">
					<Label for="ct-cpu">CPU</Label>
					<Input id="ct-cpu" bind:value={ctCpu} class="text-sm" />
				</div>
				<div class="space-y-1.5">
					<Label for="ct-mem">Memory MB</Label>
					<Input id="ct-mem" type="number" bind:value={ctMem} class="text-sm" />
				</div>
				<div class="space-y-1.5">
					<Label for="ct-timeout">Timeout s</Label>
					<Input id="ct-timeout" type="number" bind:value={ctTimeout} class="text-sm" />
				</div>
			</div>
			{#if ctBackend === 'k8s'}
				<div class="grid grid-cols-2 gap-2">
					<div class="space-y-1.5">
						<Label for="ct-ns">Namespace</Label>
						<Input id="ct-ns" bind:value={ctNamespace} placeholder="oarlock" class="text-sm" />
					</div>
					<div class="space-y-1.5">
						<Label for="ct-rc">RuntimeClass</Label>
						<Input id="ct-rc" bind:value={ctRuntimeClass} placeholder="gvisor" class="text-sm" />
					</div>
				</div>
			{/if}
			<div class="space-y-1.5">
				<Label for="ct-allow">Image allowlist</Label>
				<Input id="ct-allow" bind:value={ctAllowlist} placeholder="empty = any; or e.g. ghcr.io/acme/" class="font-mono text-xs" />
				<p class="text-muted-foreground text-xs">Comma/newline separated prefixes. Empty allows any image.</p>
			</div>
			{#if registrySecrets.length > 0}
				<div class="space-y-1.5">
					<Label>Registry credential (optional)</Label>
					<Select.Root type="single" bind:value={ctRegistry}>
						<Select.Trigger class="w-full">{ctRegistry || 'None (public images)'}</Select.Trigger>
						<Select.Content>
							<Select.Item value="">None (public images)</Select.Item>
							{#each registrySecrets as s (s.id)}
								<Select.Item value={s.name}>{s.name}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
			{/if}
			{#if ctError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{ctError}
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button onclick={saveCT} disabled={ctSaving || !ctName.trim()}>
				{ctSaving ? 'Saving…' : 'Add target'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
