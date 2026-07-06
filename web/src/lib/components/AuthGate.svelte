<script lang="ts">
	import { api } from '$lib/api';
	import { session, type AuthStatus } from '$lib/session.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';

	// Which screen to show. change-password reuses this card in a mode where
	// the user is already authed but must set a new password.
	let { mode }: { mode: Extract<AuthStatus, 'login' | 'setup' | 'change-password'> } = $props();

	let email = $state('');
	let name = $state('');
	let password = $state('');
	let confirm = $state('');
	let submitting = $state(false);
	let error = $state('');

	let copy = $derived({
		setup: {
			title: 'Welcome to oarlock',
			subtitle: 'Create the admin account to get started. This first account owns the workspace.',
			cta: 'Create admin account'
		},
		login: {
			title: 'Sign in',
			subtitle: 'Enter your credentials to continue.',
			cta: 'Sign in'
		},
		'change-password': {
			title: 'Set a new password',
			subtitle: 'Your account requires a new password before you can continue.',
			cta: 'Update password'
		}
	}[mode]);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (submitting) return;
		error = '';
		if ((mode === 'setup' || mode === 'change-password') && password !== confirm) {
			error = 'Passwords do not match.';
			return;
		}
		submitting = true;
		try {
			if (mode === 'setup') {
				await api.setup(email.trim(), name.trim(), password);
			} else if (mode === 'login') {
				await api.login(email.trim(), password);
			} else {
				// Forced change: no current password required (server allows it
				// while must_change_password is set).
				await api.changePassword(password);
			}
			await session.refresh();
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			submitting = false;
		}
	}
</script>

<div class="bg-background flex min-h-svh items-center justify-center px-4">
	<div class="w-full max-w-sm">
		<div class="mb-6 flex items-center gap-2.5">
			<span class="bg-primary text-primary-foreground flex size-9 shrink-0 items-center justify-center rounded-lg text-lg shadow-sm">
				🛶
			</span>
			<span class="text-lg font-semibold tracking-tight">oarlock</span>
		</div>

		<h1 class="text-xl font-semibold tracking-tight">{copy.title}</h1>
		<p class="text-muted-foreground mt-1 text-sm">{copy.subtitle}</p>

		<form onsubmit={submit} class="mt-6 space-y-4">
			{#if mode === 'setup' || mode === 'login'}
				<div class="space-y-1.5">
					<Label for="email">Email</Label>
					<Input id="email" type="email" bind:value={email} required autocomplete="username" placeholder="you@example.com" />
				</div>
			{/if}
			{#if mode === 'setup'}
				<div class="space-y-1.5">
					<Label for="name">Name <span class="text-muted-foreground">(optional)</span></Label>
					<Input id="name" bind:value={name} autocomplete="name" placeholder="Ada Lovelace" />
				</div>
			{/if}
			<div class="space-y-1.5">
				<Label for="password">{mode === 'login' ? 'Password' : 'New password'}</Label>
				<Input
					id="password"
					type="password"
					bind:value={password}
					required
					minlength={mode === 'login' ? undefined : 8}
					autocomplete={mode === 'login' ? 'current-password' : 'new-password'}
					placeholder={mode === 'login' ? '' : 'At least 8 characters'}
				/>
			</div>
			{#if mode === 'setup' || mode === 'change-password'}
				<div class="space-y-1.5">
					<Label for="confirm">Confirm password</Label>
					<Input id="confirm" type="password" bind:value={confirm} required autocomplete="new-password" />
				</div>
			{/if}

			{#if error}
				<div class="border-destructive/30 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm">
					{error}
				</div>
			{/if}

			<Button type="submit" class="w-full" disabled={submitting}>
				{submitting ? 'Please wait…' : copy.cta}
			</Button>
		</form>

		{#if mode === 'change-password'}
			<button
				type="button"
				class="text-muted-foreground hover:text-foreground mt-4 text-xs"
				onclick={() => session.logout()}
			>
				Sign out instead
			</button>
		{/if}
	</div>
</div>
