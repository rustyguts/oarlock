<script lang="ts">
	import {
		api,
		API_BASE,
		type Trigger,
		type ScheduleTriggerConfig,
		type WebhookTriggerConfig
	} from '$lib/api';
	import { fmtRelative } from '$lib/flow';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import ZapIcon from '@lucide/svelte/icons/zap';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import WebhookIcon from '@lucide/svelte/icons/webhook';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import CheckIcon from '@lucide/svelte/icons/check';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let {
		open = $bindable(false),
		workflowId,
		workflowEnabled = true
	}: {
		open?: boolean;
		workflowId: string;
		workflowEnabled?: boolean;
	} = $props();

	// The webhook ingress lives at /hooks/{workspace-slug}/{path}. The slug
	// comes from /v1/me; the placeholder only shows against an older API.
	const WS_SLUG_PLACEHOLDER = '<workspace-slug>';
	let wsSlug = $state<string | null>(null);
	const PATH_RE = /^[a-z0-9][a-z0-9-]*$/;

	let triggers = $state<Trigger[]>([]);
	let loading = $state(false);
	let error = $state('');

	// Add-trigger dialog state machine: chooser → schedule|webhook form →
	// (webhook) success screen with the ingress URL.
	let addOpen = $state(false);
	let addType = $state<'schedule' | 'webhook' | null>(null);
	let cronDraft = $state('');
	let pathDraft = $state('');
	let secretDraft = $state('');
	let createError = $state('');
	let creating = $state(false);
	// Set once a webhook is created so we can show its ingress URL.
	let createdPath = $state<string | null>(null);
	let createdHasSecret = $state(false);

	// Delete confirm.
	let confirmOpen = $state(false);
	let pendingDelete = $state<Trigger | null>(null);

	// Transient "Copied" affordance keyed by the copied value.
	let copied = $state<string | null>(null);
	let copyTimer: ReturnType<typeof setTimeout> | null = null;

	async function load() {
		loading = true;
		error = '';
		try {
			triggers = await api.listTriggers(workflowId);
			if (wsSlug === null) {
				wsSlug = (await api.me()).workspace.slug ?? null;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// Refetch each time the sheet opens so it reflects changes made elsewhere.
	$effect(() => {
		if (open) load();
	});

	function scheduleCron(t: Trigger): string {
		return (t.config as ScheduleTriggerConfig).cron ?? '';
	}
	function webhookPath(t: Trigger): string {
		return (t.config as WebhookTriggerConfig).path ?? '';
	}
	function webhookHasSecret(t: Trigger): boolean {
		return !!(t.config as WebhookTriggerConfig).secret;
	}

	function ingressUrl(path: string): string {
		return `${API_BASE}/hooks/${wsSlug ?? WS_SLUG_PLACEHOLDER}/${path}`;
	}

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

	function openAdd() {
		addType = null;
		cronDraft = '';
		pathDraft = '';
		secretDraft = '';
		createError = '';
		createdPath = null;
		createdHasSecret = false;
		addOpen = true;
	}

	async function createSchedule() {
		const cron = cronDraft.trim();
		if (!cron) {
			createError = 'Enter a cron expression.';
			return;
		}
		creating = true;
		createError = '';
		try {
			await api.createTrigger(workflowId, { type: 'schedule', config: { cron } });
			addOpen = false;
			await load();
		} catch (e) {
			createError = e instanceof Error ? e.message : String(e);
		} finally {
			creating = false;
		}
	}

	async function createWebhook() {
		const path = pathDraft.trim();
		if (!PATH_RE.test(path)) {
			createError = 'Path must match ^[a-z0-9][a-z0-9-]*$ (lowercase letters, digits, dashes).';
			return;
		}
		const secret = secretDraft.trim();
		creating = true;
		createError = '';
		try {
			await api.createTrigger(workflowId, {
				type: 'webhook',
				config: secret ? { path, secret } : { path }
			});
			// Stay in the dialog to show the ingress URL.
			createdPath = path;
			createdHasSecret = !!secret;
			await load();
		} catch (e) {
			createError = e instanceof Error ? e.message : String(e);
		} finally {
			creating = false;
		}
	}

	// Optimistic enable/disable — flip locally, PATCH, revert on failure.
	async function toggle(t: Trigger, next: boolean) {
		const prev = t.is_enabled;
		triggers = triggers.map((x) => (x.id === t.id ? { ...x, is_enabled: next } : x));
		try {
			await api.patchTrigger(t.id, { is_enabled: next });
			error = '';
		} catch (e) {
			triggers = triggers.map((x) => (x.id === t.id ? { ...x, is_enabled: prev } : x));
			error = e instanceof Error ? e.message : String(e);
		}
	}

	function askDelete(t: Trigger) {
		pendingDelete = t;
		confirmOpen = true;
	}

	async function doDelete() {
		const t = pendingDelete;
		if (!t) return;
		try {
			await api.deleteTrigger(t.id);
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			pendingDelete = null;
		}
	}
</script>

<Sheet.Root bind:open>
	<Sheet.Content side="right" class="w-full gap-0 p-0 sm:max-w-md">
		<Sheet.Header class="border-b px-4 py-3">
			<Sheet.Title class="flex items-center gap-2">
				<ZapIcon class="text-primary-strong size-4" />
				Triggers
			</Sheet.Title>
			<Sheet.Description>
				Fire this workflow automatically — on a schedule, or when a webhook is called.
			</Sheet.Description>
		</Sheet.Header>

		{#if !workflowEnabled}
			<div
				class="text-muted-foreground flex items-start gap-2 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2.5 text-xs"
			>
				<TriangleAlertIcon class="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
				<span>This workflow is disabled — triggers won't fire. Enable it from the workflows list.</span>
			</div>
		{/if}

		<div class="border-b px-4 py-3">
			<Button variant="outline" size="sm" class="w-full" onclick={openAdd}>
				<PlusIcon class="size-4" /> Add trigger
			</Button>
		</div>

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if loading}
				<p class="text-muted-foreground p-4 text-sm">Loading…</p>
			{:else if error}
				<div
					class="border-destructive/30 bg-destructive/10 text-destructive m-4 rounded-md border px-3 py-2 text-sm"
				>
					{error}
				</div>
			{:else if triggers.length === 0}
				<p class="text-muted-foreground p-4 text-sm">
					No triggers yet — add one to fire this workflow automatically.
				</p>
			{:else}
				<div class="divide-border divide-y">
					{#each triggers as t (t.id)}
						<div class="flex items-center gap-3 px-4 py-3">
							<span
								class="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-md"
							>
								{#if t.type === 'schedule'}
									<ClockIcon class="size-4" />
								{:else}
									<WebhookIcon class="size-4" />
								{/if}
							</span>
							<div class="min-w-0 flex-1">
								<div class="flex items-center gap-2">
									<span class="text-xs font-medium capitalize">{t.type}</span>
									{#if t.type === 'webhook' && webhookHasSecret(t)}
										<span class="text-muted-foreground/70 text-[10px] tracking-wide uppercase">signed</span>
									{/if}
								</div>
								<div class="text-muted-foreground mt-0.5 truncate font-mono text-xs">
									{#if t.type === 'schedule'}
										{scheduleCron(t)}
									{:else}
										/{webhookPath(t)}
									{/if}
								</div>
							</div>
							<div class="flex shrink-0 items-center gap-1.5">
								{#if t.type === 'webhook'}
									{@const url = ingressUrl(webhookPath(t))}
									<Button
										variant="ghost"
										size="icon"
										class="size-8"
										aria-label="Copy webhook URL"
										title="Copy webhook URL"
										onclick={() => copy(url)}
									>
										{#if copied === url}
											<CheckIcon class="size-4 text-emerald-600 dark:text-emerald-400" />
										{:else}
											<CopyIcon class="size-4" />
										{/if}
									</Button>
								{/if}
								<Switch
									checked={t.is_enabled}
									onCheckedChange={(v) => toggle(t, v)}
									aria-label={t.is_enabled ? 'Disable trigger' : 'Enable trigger'}
								/>
								<Button
									variant="ghost"
									size="icon"
									class="text-muted-foreground hover:text-destructive size-8"
									aria-label="Delete trigger"
									onclick={() => askDelete(t)}
								>
									<Trash2Icon class="size-4" />
								</Button>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>

<!-- Add-trigger dialog -->
<Dialog.Root bind:open={addOpen}>
	<Dialog.Content class="sm:max-w-md">
		{#if createdPath}
			<!-- Webhook created: show the ingress URL -->
			{@const url = ingressUrl(createdPath)}
			<Dialog.Header>
				<Dialog.Title class="flex items-center gap-2">
					<WebhookIcon class="text-primary-strong size-4" /> Webhook created
				</Dialog.Title>
				<Dialog.Description>Send a POST to this URL to fire the workflow.</Dialog.Description>
			</Dialog.Header>
			<div class="mt-3 space-y-3">
				<div class="bg-muted flex items-start gap-2 rounded-md p-2.5">
					<code class="min-w-0 flex-1 font-mono text-xs break-all">
						POST {url}
					</code>
					<Button
						variant="ghost"
						size="icon"
						class="size-7 shrink-0"
						aria-label="Copy webhook URL"
						onclick={() => copy(url)}
					>
						{#if copied === url}
							<CheckIcon class="size-4 text-emerald-600 dark:text-emerald-400" />
						{:else}
							<CopyIcon class="size-4" />
						{/if}
					</Button>
				</div>
				{#if wsSlug === null}
					<p class="text-muted-foreground/80 text-[11px] leading-relaxed">
						Replace <code class="bg-muted rounded px-1">{WS_SLUG_PLACEHOLDER}</code> with your
						workspace slug (<code class="bg-muted rounded px-1">default</code> for the initial
						workspace) — this API build doesn't expose it.
					</p>
				{/if}
				{#if createdHasSecret}
					<p class="text-muted-foreground/80 text-[11px] leading-relaxed">
						This webhook is signed. Send header
						<code class="bg-muted rounded px-1">X-Oarlock-Signature: hex(HMAC-SHA256(body, secret))</code>
						with each request.
					</p>
				{/if}
			</div>
			<Dialog.Footer class="mt-4">
				<Button onclick={() => (addOpen = false)}>Done</Button>
			</Dialog.Footer>
		{:else if addType === null}
			<!-- Type chooser -->
			<Dialog.Header>
				<Dialog.Title>Add trigger</Dialog.Title>
				<Dialog.Description>Choose how this workflow gets fired.</Dialog.Description>
			</Dialog.Header>
			<div class="mt-3 grid grid-cols-2 gap-3">
				<button
					type="button"
					onclick={() => ((addType = 'schedule'), (createError = ''))}
					class="hover:border-primary/60 flex flex-col items-start gap-2 rounded-lg border p-3 text-left transition-colors"
				>
					<span class="bg-primary/12 text-primary-strong flex size-9 items-center justify-center rounded-lg">
						<ClockIcon class="size-4.5" />
					</span>
					<span class="text-sm font-semibold">Schedule</span>
					<span class="text-muted-foreground text-xs leading-snug">Run on a recurring cron.</span>
				</button>
				<button
					type="button"
					onclick={() => ((addType = 'webhook'), (createError = ''))}
					class="hover:border-primary/60 flex flex-col items-start gap-2 rounded-lg border p-3 text-left transition-colors"
				>
					<span class="bg-primary/12 text-primary-strong flex size-9 items-center justify-center rounded-lg">
						<WebhookIcon class="size-4.5" />
					</span>
					<span class="text-sm font-semibold">Webhook</span>
					<span class="text-muted-foreground text-xs leading-snug">Run on an HTTP POST.</span>
				</button>
			</div>
		{:else if addType === 'schedule'}
			<!-- Schedule form -->
			<Dialog.Header>
				<Dialog.Title class="flex items-center gap-2">
					<ClockIcon class="text-primary-strong size-4" /> Schedule trigger
				</Dialog.Title>
				<Dialog.Description>Fire the workflow on a recurring cron.</Dialog.Description>
			</Dialog.Header>
			<div class="mt-3 space-y-2">
				<label class="block">
					<span class="text-muted-foreground text-xs">Cron expression</span>
					<Input bind:value={cronDraft} placeholder="*/5 * * * *" class="mt-1 font-mono text-sm" />
					<span class="text-muted-foreground/70 mt-1 block font-mono text-[11px]">
						minute hour day month weekday
					</span>
				</label>
				{#if createError}
					<p class="text-destructive text-xs">{createError}</p>
				{/if}
			</div>
			<Dialog.Footer class="mt-4">
				<Button variant="outline" onclick={() => (addType = null)}>Back</Button>
				<Button onclick={createSchedule} disabled={creating}>
					{creating ? 'Creating…' : 'Create'}
				</Button>
			</Dialog.Footer>
		{:else}
			<!-- Webhook form -->
			<Dialog.Header>
				<Dialog.Title class="flex items-center gap-2">
					<WebhookIcon class="text-primary-strong size-4" /> Webhook trigger
				</Dialog.Title>
				<Dialog.Description>Fire the workflow on an HTTP POST.</Dialog.Description>
			</Dialog.Header>
			<div class="mt-3 space-y-3">
				<label class="block">
					<span class="text-muted-foreground text-xs">Path</span>
					<Input bind:value={pathDraft} placeholder="my-hook" class="mt-1 font-mono text-sm" />
					<span class="text-muted-foreground/70 mt-1 block text-[11px]">
						Lowercase letters, digits, dashes. Part of the ingress URL.
					</span>
				</label>
				<label class="block">
					<span class="text-muted-foreground text-xs">Secret (optional)</span>
					<Input
						bind:value={secretDraft}
						placeholder="signing secret"
						class="mt-1 font-mono text-sm"
					/>
					<span class="text-muted-foreground/70 mt-1 block text-[11px]">
						When set, callers must send an <code class="bg-muted rounded px-1">X-Oarlock-Signature</code>
						HMAC header.
					</span>
				</label>
				{#if createError}
					<p class="text-destructive text-xs">{createError}</p>
				{/if}
			</div>
			<Dialog.Footer class="mt-4">
				<Button variant="outline" onclick={() => (addType = null)}>Back</Button>
				<Button onclick={createWebhook} disabled={creating}>
					{creating ? 'Creating…' : 'Create'}
				</Button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Delete trigger?"
	description={pendingDelete
		? pendingDelete.type === 'schedule'
			? `The schedule "${scheduleCron(pendingDelete)}" will stop firing this workflow.`
			: `The webhook "/${webhookPath(pendingDelete)}" will stop firing this workflow.`
		: ''}
	confirmText="Delete"
	onconfirm={doDelete}
/>
