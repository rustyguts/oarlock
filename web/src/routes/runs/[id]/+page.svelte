<script lang="ts">
	import { page } from '$app/state';
	import RunDetail from '$lib/components/RunDetail.svelte';
</script>

{#key page.params.id}
	<RunDetail runId={page.params.id!} />
{/key}
