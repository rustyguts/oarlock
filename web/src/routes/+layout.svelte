<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { ModeWatcher, toggleMode } from 'mode-watcher';
	import SunIcon from '@lucide/svelte/icons/sun';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';
	import AppSidebar from '$lib/components/AppSidebar.svelte';
	import AuthGate from '$lib/components/AuthGate.svelte';
	import { session } from '$lib/session.svelte';

	let { children } = $props();
	let me = $derived(session.me);

	let section = $derived.by(() => {
		const p = page.url.pathname;
		if (p.startsWith('/mcp')) return 'MCP Servers';
		if (p.startsWith('/api-access')) return 'API Access';
		if (p.startsWith('/users')) return 'Users';
		if (p.startsWith('/configuration')) return 'Configuration';
		if (p.startsWith('/runs/')) return 'Run detail';
		if (p.includes('/runs')) return 'Run history';
		if (p.startsWith('/workflows/')) return 'Editor';
		if (p === '/workflows') return 'Workflows';
		return 'Dashboard';
	});

	onMount(() => session.refresh());
</script>

<ModeWatcher />

{#if session.status === 'loading'}
	<!-- Brief; avoids a flash of the login screen before /v1/me resolves. -->
	<div class="bg-background min-h-svh"></div>
{:else if session.status === 'authed'}
	<Sidebar.Provider>
		<AppSidebar {me} />
		<Sidebar.Inset class="h-svh min-w-0">
			<header class="bg-background flex h-12 shrink-0 items-center gap-2 border-b px-3">
				<Sidebar.Trigger class="text-muted-foreground" />
				<Separator orientation="vertical" class="!h-4" />
				<span class="text-sm font-medium">{section}</span>
				<div class="ml-auto flex items-center gap-2">
					{#if me}
						<span class="text-muted-foreground text-xs">{me.workspace.name}</span>
					{/if}
					<Button onclick={toggleMode} variant="ghost" size="icon" aria-label="Toggle theme">
						<SunIcon class="size-4 scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" />
						<MoonIcon class="absolute size-4 scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" />
					</Button>
				</div>
			</header>
			<main class="min-h-0 flex-1 overflow-y-auto">
				{@render children()}
			</main>
		</Sidebar.Inset>
	</Sidebar.Provider>
{:else if session.status === 'error'}
	<div class="bg-background flex min-h-svh flex-col items-center justify-center gap-3 px-4 text-center">
		<p class="text-sm font-medium">Couldn't reach the oarlock API.</p>
		<p class="text-muted-foreground text-xs">{session.error}</p>
		<Button variant="outline" onclick={() => session.refresh()}>Retry</Button>
	</div>
{:else}
	<AuthGate mode={session.status} />
{/if}
