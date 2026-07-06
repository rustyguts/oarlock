<script lang="ts">
	import { page } from '$app/state';
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { isAdminRole, type Me } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import LayoutDashboardIcon from '@lucide/svelte/icons/layout-dashboard';
	import WorkflowIcon from '@lucide/svelte/icons/workflow';
	import ServerIcon from '@lucide/svelte/icons/server';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import Settings2Icon from '@lucide/svelte/icons/settings-2';
	import UsersIcon from '@lucide/svelte/icons/users';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import KeySquareIcon from '@lucide/svelte/icons/key-square';

	let { me = null }: { me?: Me | null } = $props();

	let admin = $derived(isAdminRole(me?.role));
	let items = $derived(
		[
			{ title: 'Dashboard', href: '/', icon: LayoutDashboardIcon, show: true },
			{ title: 'Workflows', href: '/workflows', icon: WorkflowIcon, show: true },
			{ title: 'MCP Servers', href: '/mcp', icon: ServerIcon, show: true },
			{ title: 'API Access', href: '/api-access', icon: KeyRoundIcon, show: admin },
			{ title: 'Users', href: '/users', icon: UsersIcon, show: admin },
			{ title: 'Configuration', href: '/configuration', icon: Settings2Icon, show: true }
		].filter((i) => i.show)
	);

	function isActive(href: string): boolean {
		const path = page.url.pathname;
		if (href === '/') return path === '/';
		if (href === '/workflows') {
			return path.startsWith('/workflows') || path.startsWith('/runs');
		}
		return path === href || path.startsWith(href + '/');
	}

	let menuOpen = $state(false);
	let initials = $derived(
		(me?.user?.name ?? me?.user?.email ?? '?')
			.split(/[\s@._-]+/)
			.filter(Boolean)
			.slice(0, 2)
			.map((w) => w[0]?.toUpperCase())
			.join('')
	);
</script>

<Sidebar.Root collapsible="icon">
	<Sidebar.Header class="px-3 pt-3 group-data-[collapsible=icon]:px-2">
		<a href="/" class="flex items-center gap-2.5 font-semibold tracking-tight">
			<span class="bg-primary text-primary-foreground flex size-8 shrink-0 items-center justify-center rounded-lg text-base shadow-sm">
				🛶
			</span>
			<span class="truncate text-base group-data-[collapsible=icon]:hidden">oarlock</span>
		</a>
	</Sidebar.Header>

	<Sidebar.Content class="px-2 pt-2">
		<Sidebar.Group class="p-0">
			<Sidebar.GroupLabel class="text-sidebar-foreground/50 px-2">Platform</Sidebar.GroupLabel>
			<Sidebar.GroupContent>
				<Sidebar.Menu class="gap-1.5">
					{#each items as item (item.href)}
						{@const active = isActive(item.href)}
						<Sidebar.MenuItem>
							<Sidebar.MenuButton
								isActive={active}
								tooltipContent={item.title}
								class="text-sidebar-foreground/70 hover:text-sidebar-foreground h-9 rounded-lg px-3
								data-active:bg-primary/10 data-active:text-primary-strong data-active:font-semibold
								data-active:hover:bg-primary/15 data-active:hover:text-primary-strong"
							>
								{#snippet child({ props })}
									<a href={item.href} {...props}>
										<item.icon class="size-4 shrink-0" />
										<span>{item.title}</span>
									</a>
								{/snippet}
							</Sidebar.MenuButton>
						</Sidebar.MenuItem>
					{/each}
				</Sidebar.Menu>
			</Sidebar.GroupContent>
		</Sidebar.Group>
	</Sidebar.Content>

	<Sidebar.Footer class="border-sidebar-border border-t p-3 group-data-[collapsible=icon]:p-2">
		{#if me?.user}
			<div class="relative">
				{#if menuOpen}
					<button
						type="button"
						class="fixed inset-0 z-10 cursor-default"
						aria-label="Close menu"
						onclick={() => (menuOpen = false)}
					></button>
					<div class="bg-popover absolute bottom-full z-20 mb-2 w-full overflow-hidden rounded-lg border shadow-md group-data-[collapsible=icon]:hidden">
						<a
							href="/account"
							class="hover:bg-muted flex items-center gap-2 px-3 py-2 text-sm"
							onclick={() => (menuOpen = false)}
						>
							<KeySquareIcon class="size-4" /> Change password
						</a>
						<button
							type="button"
							class="hover:bg-muted text-destructive flex w-full items-center gap-2 px-3 py-2 text-sm"
							onclick={() => session.logout()}
						>
							<LogOutIcon class="size-4" /> Sign out
						</button>
					</div>
				{/if}
				<button
					type="button"
					class="hover:bg-muted/60 flex w-full items-center gap-2.5 rounded-md p-1 text-left"
					onclick={() => (menuOpen = !menuOpen)}
				>
					<span class="bg-muted text-foreground flex size-8 shrink-0 items-center justify-center rounded-full text-xs font-semibold">
						{initials}
					</span>
					<div class="min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
						<div class="flex items-center gap-1.5">
							<span class="truncate text-sm font-medium">{me.user.name ?? me.user.email}</span>
							<Badge variant="secondary" class="h-4 px-1.5 text-[10px]">{me.role}</Badge>
						</div>
						<div class="text-muted-foreground truncate text-xs">{me.workspace.name}</div>
					</div>
				</button>
			</div>
		{/if}
	</Sidebar.Footer>
	<Sidebar.Rail />
</Sidebar.Root>
