<script lang="ts">
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Card from '$lib/components/ui/card/index.js';

	let me = $derived(session.me);

	let current = $state('');
	let next = $state('');
	let confirm = $state('');
	let saving = $state(false);
	let error = $state('');
	let notice = $state('');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (saving) return;
		error = '';
		notice = '';
		if (next !== confirm) {
			error = 'New passwords do not match.';
			return;
		}
		saving = true;
		try {
			await api.changePassword(next, current);
			current = next = confirm = '';
			notice = 'Password updated.';
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			saving = false;
		}
	}
</script>

<div class="w-full px-6 py-6">
	<div class="mb-6">
		<h1 class="text-xl font-semibold">Account</h1>
		<p class="text-muted-foreground text-sm">
			{me?.user?.email}{me?.role ? ` · ${me.role}` : ''}
		</p>
	</div>

	<Card.Root class="max-w-md py-5">
		<Card.Content class="px-5">
			<h2 class="font-medium">Change password</h2>
			<form onsubmit={submit} class="mt-4 space-y-4">
				<div class="space-y-1.5">
					<Label for="current">Current password</Label>
					<Input id="current" type="password" bind:value={current} required autocomplete="current-password" />
				</div>
				<div class="space-y-1.5">
					<Label for="next">New password</Label>
					<Input id="next" type="password" bind:value={next} required minlength={8} autocomplete="new-password" placeholder="At least 8 characters" />
				</div>
				<div class="space-y-1.5">
					<Label for="confirm">Confirm new password</Label>
					<Input id="confirm" type="password" bind:value={confirm} required autocomplete="new-password" />
				</div>
				{#if error}
					<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm">
						{error}
					</div>
				{/if}
				{#if notice}
					<p class="text-sm text-emerald-600 dark:text-emerald-400">{notice}</p>
				{/if}
				<Button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Update password'}</Button>
			</form>
			<p class="text-muted-foreground/70 mt-4 text-xs">
				Changing your password signs out your other sessions.
			</p>
		</Card.Content>
	</Card.Root>
</div>
