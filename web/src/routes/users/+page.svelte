<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, type User } from '$lib/api';
	import { session } from '$lib/session.svelte';
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
	import UsersIcon from '@lucide/svelte/icons/users';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
	import ShieldIcon from '@lucide/svelte/icons/shield';

	const ROLES = [
		{ value: 'admin', label: 'Admin', description: 'Full access, including users and API tokens' },
		{ value: 'editor', label: 'Editor', description: 'Build and run workflows' },
		{ value: 'viewer', label: 'Viewer', description: 'Read-only access' }
	];
	function roleLabel(role: string): string {
		return ROLES.find((r) => r.value === role)?.label ?? role;
	}

	let me = $derived(session.me);
	let users = $state<User[]>([]);
	let error = $state('');
	let loading = $state(true);
	let notice = $state('');
	let noticeTimer: ReturnType<typeof setTimeout> | null = null;
	function flashOk(msg: string) {
		notice = msg;
		if (noticeTimer) clearTimeout(noticeTimer);
		noticeTimer = setTimeout(() => (notice = ''), 2500);
	}

	// Create dialog
	let open = $state(false);
	let email = $state('');
	let name = $state('');
	let password = $state('');
	let role = $state('editor');
	let saving = $state(false);
	let dialogError = $state('');

	// Reset-password dialog
	let resetOpen = $state(false);
	let resetTarget = $state<User | null>(null);
	let resetValue = $state('');
	let resetting = $state(false);
	let resetError = $state('');

	let confirmOpen = $state(false);
	let pendingDelete = $state<User | null>(null);

	async function refresh() {
		try {
			users = await api.listUsers();
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}
	onMount(refresh);

	function openCreate() {
		email = '';
		name = '';
		password = '';
		role = 'editor';
		dialogError = '';
		open = true;
	}

	async function save(e?: SubmitEvent) {
		e?.preventDefault();
		if (saving || !email.trim() || !password.trim()) return;
		saving = true;
		dialogError = '';
		try {
			await api.createUser({ email: email.trim(), name: name.trim(), password: password.trim(), role });
			open = false;
			await refresh();
			flashOk(`Added ${email.trim()}`);
		} catch (err) {
			dialogError = err instanceof Error ? err.message : String(err);
		} finally {
			saving = false;
		}
	}

	async function changeRole(u: User, nextRole: string) {
		if (nextRole === u.role) return;
		const prev = u.role;
		u.role = nextRole;
		try {
			await api.updateUser(u.id, { role: nextRole });
			error = '';
		} catch (e) {
			u.role = prev;
			error = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
			await refresh();
		}
	}

	function openReset(u: User) {
		resetTarget = u;
		resetValue = '';
		resetError = '';
		resetOpen = true;
	}
	async function doReset(e?: SubmitEvent) {
		e?.preventDefault();
		const u = resetTarget;
		if (!u || resetting || !resetValue.trim()) return;
		resetting = true;
		resetError = '';
		try {
			await api.resetUserPassword(u.id, resetValue.trim());
			resetOpen = false;
			flashOk(`Reset password for ${u.email}`);
		} catch (err) {
			resetError = err instanceof Error ? err.message : String(err);
		} finally {
			resetting = false;
		}
	}

	function remove(u: User) {
		pendingDelete = u;
		confirmOpen = true;
	}
	async function confirmDelete() {
		const u = pendingDelete;
		if (!u) return;
		try {
			await api.deleteUser(u.id);
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
			<h1 class="text-xl font-semibold">Users</h1>
			<p class="text-muted-foreground text-sm">People who can sign in to this workspace.</p>
		</div>
		<div class="flex items-center gap-3">
			{#if notice}<span class="text-xs text-emerald-600 dark:text-emerald-400">{notice}</span>{/if}
			<Button onclick={openCreate}><PlusIcon class="size-4" /> Add user</Button>
		</div>
	</div>

	{#if error}
		<div class="border-destructive/30 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
			{error}
		</div>
	{/if}

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<Card.Root class="py-0">
			<Card.Content class="divide-border divide-y px-0">
				{#each users as u (u.id)}
					<div class="flex items-center gap-4 px-4 py-3">
						<div class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
							{#if u.role === 'owner' || u.role === 'admin'}
								<ShieldIcon class="size-4" />
							{:else}
								<UsersIcon class="size-4" />
							{/if}
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2">
								<span class="font-medium">{u.name || u.email}</span>
								{#if u.id === me?.user?.id}<Badge variant="secondary">you</Badge>{/if}
								{#if u.must_change_password}
									<Badge variant="outline" class="text-amber-600 dark:text-amber-400">must reset</Badge>
								{/if}
							</div>
							<div class="text-muted-foreground mt-0.5 flex items-center gap-2 text-xs">
								<span>{u.email}</span>
								<span>·</span>
								<span>{u.last_seen_at ? `active ${fmtRelative(u.last_seen_at)}` : 'never signed in'}</span>
							</div>
						</div>
						<div class="flex shrink-0 items-center gap-2">
							{#if u.role === 'owner'}
								<Badge variant="secondary">owner</Badge>
							{:else}
								<Select.Root type="single" value={u.role} onValueChange={(v: string) => changeRole(u, v)}>
									<Select.Trigger class="h-8 w-28">{roleLabel(u.role)}</Select.Trigger>
									<Select.Content>
										{#each ROLES as r (r.value)}
											<Select.Item value={r.value}>{r.label}</Select.Item>
										{/each}
									</Select.Content>
								</Select.Root>
							{/if}
							<Button variant="outline" size="sm" onclick={() => openReset(u)}>
								<RotateCwIcon class="size-4" /> Reset…
							</Button>
							<Button
								variant="ghost"
								size="icon"
								class="text-muted-foreground hover:text-destructive"
								onclick={() => remove(u)}
								disabled={u.id === me?.user?.id}
								aria-label="Delete user"
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
				<Dialog.Title>Add user</Dialog.Title>
				<Dialog.Description>
					The user signs in with this password, then is asked to choose a new one.
				</Dialog.Description>
			</Dialog.Header>
			<div class="space-y-4">
				<div class="space-y-1.5">
					<Label for="u-email">Email</Label>
					<Input id="u-email" type="email" bind:value={email} placeholder="teammate@example.com" />
				</div>
				<div class="space-y-1.5">
					<Label for="u-name">Name <span class="text-muted-foreground">(optional)</span></Label>
					<Input id="u-name" bind:value={name} placeholder="Grace Hopper" />
				</div>
				<div class="space-y-1.5">
					<Label>Role</Label>
					<Select.Root type="single" bind:value={role}>
						<Select.Trigger class="w-full">{roleLabel(role)}</Select.Trigger>
						<Select.Content>
							{#each ROLES as r (r.value)}
								<Select.Item value={r.value}>{r.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
					<p class="text-muted-foreground text-xs">{ROLES.find((r) => r.value === role)?.description}</p>
				</div>
				<div class="space-y-1.5">
					<Label for="u-password">Initial password</Label>
					<Input id="u-password" type="password" bind:value={password} placeholder="At least 8 characters" class="font-mono text-sm" />
				</div>
				{#if dialogError}
					<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
						{dialogError}
					</div>
				{/if}
			</div>
			<Dialog.Footer>
				<Button type="submit" disabled={saving || !email.trim() || !password.trim()}>
					{saving ? 'Adding…' : 'Add user'}
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={resetOpen}>
	<Dialog.Content class="sm:max-w-md">
		<form class="contents" onsubmit={doReset}>
			<Dialog.Header>
				<Dialog.Title>Reset password</Dialog.Title>
				<Dialog.Description>
					Set a temporary password for
					<code class="bg-muted rounded px-1 font-mono">{resetTarget?.email}</code>. They'll be signed
					out and asked to choose a new one on next login.
				</Dialog.Description>
			</Dialog.Header>
			<div class="space-y-1.5">
				<Label for="reset-value">Temporary password</Label>
				<Input id="reset-value" type="password" bind:value={resetValue} placeholder="At least 8 characters" class="font-mono text-sm" />
			</div>
			{#if resetError}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border p-3 text-xs">
					{resetError}
				</div>
			{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (resetOpen = false)}>Cancel</Button>
				<Button type="submit" disabled={resetting || !resetValue.trim()}>
					{resetting ? 'Resetting…' : 'Reset password'}
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Remove user?"
	description={pendingDelete
		? `"${pendingDelete.email}" will lose access and be signed out everywhere.`
		: ''}
	confirmText="Remove"
	onconfirm={confirmDelete}
/>
