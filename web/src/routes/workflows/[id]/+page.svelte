<script lang="ts">
	import { SvelteFlowProvider } from '@xyflow/svelte';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import Editor from '$lib/components/Editor.svelte';
	import { useSidebar } from '$lib/components/ui/sidebar/index.js';

	// The canvas wants the full width, so collapse the nav sidebar while the
	// editor is open and restore the prior state on the way out. The user can
	// still expand it manually via the header trigger (or ⌘/Ctrl+B).
	const sidebar = useSidebar();
	onMount(() => {
		if (sidebar.isMobile) return;
		const wasOpen = sidebar.open;
		sidebar.setOpen(false);
		return () => sidebar.setOpen(wasOpen);
	});
</script>

<SvelteFlowProvider>
	{#key page.params.id}
		<Editor workflowId={page.params.id!} />
	{/key}
</SvelteFlowProvider>
