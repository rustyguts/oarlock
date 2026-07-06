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
	import { api, type Me } from '$lib/api';

	let { children } = $props();
	let me = $state<Me | null>(null);

	let section = $derived.by(() => {
		const p = page.url.pathname;
		if (p.startsWith('/mcp')) return 'MCP Servers';
		if (p.startsWith('/api-access')) return 'MCP Access';
		if (p.startsWith('/configuration')) return 'Configuration';
		if (p.startsWith('/runs/')) return 'Run detail';
		if (p.includes('/runs')) return 'Run history';
		if (p.startsWith('/workflows/')) return 'Editor';
		if (p === '/workflows') return 'Workflows';
		return 'Dashboard';
	});

	onMount(async () => {
		try {
			me = await api.me(); // first call auto-logs-in the bootstrap owner
		} catch {
			/* UI just omits user info if the API is down */
		}
	});
</script>

<ModeWatcher />

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
