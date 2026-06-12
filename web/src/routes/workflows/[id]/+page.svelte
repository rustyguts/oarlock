<script lang="ts">
	import { SvelteFlowProvider } from '@xyflow/svelte';
	import { page } from '$app/state';
	import Editor from '$lib/components/Editor.svelte';
</script>

<SvelteFlowProvider>
	{#key page.params.id}
		<Editor workflowId={page.params.id!} />
	{/key}
</SvelteFlowProvider>
