<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import { statusColors } from '$lib/flow';
	import { stepIcon } from '$lib/step-icons';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import type { StepNode } from '$lib/flow';

	let { id, data, selected }: NodeProps<StepNode> = $props();
	let Icon = $derived(stepIcon(data.stepType));
</script>

<div
	class="min-w-40 rounded-xl border-2 px-3 py-2.5 text-left shadow-sm transition-colors
	{data.status ? (statusColors[data.status] ?? 'border-border bg-card') : 'border-border bg-card'}
	{selected ? 'ring-primary ring-2 ring-offset-1 ring-offset-background' : ''}"
>
	<Handle type="target" position={Position.Left} class="!bg-muted-foreground !h-2.5 !w-2.5" />
	<div class="flex items-center gap-2">
		<span class="bg-primary/12 text-primary-strong flex size-6 shrink-0 items-center justify-center rounded-md">
			<Icon class="size-3.5" />
		</span>
		<span class="text-foreground truncate text-sm font-semibold tracking-tight">{id}</span>
	</div>
	<div class="text-muted-foreground mt-1.5 font-mono text-[10px] tracking-wide">
		{data.stepType}{data.retries ? ` · ${data.retries}↻` : ''}
	</div>
	{#if data.status === 'suspended'}
		<div class="mt-1 flex items-center gap-1 text-[10px] font-semibold tracking-wide text-amber-600 uppercase dark:text-amber-400">
			<PauseIcon class="size-3" /> waiting
		</div>
	{:else if data.status}
		<div class="text-foreground mt-1 text-[10px] font-semibold tracking-wide uppercase">
			{data.status}
		</div>
	{/if}
	<Handle type="source" position={Position.Right} class="!bg-muted-foreground !h-2.5 !w-2.5" />
</div>
