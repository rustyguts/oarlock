<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import { statusColors } from '$lib/flow';
	import { stepIcon } from '$lib/step-icons';
	import type { StepNode } from '$lib/flow';

	let { id, data, selected }: NodeProps<StepNode> = $props();
	let Icon = $derived(stepIcon(data.stepType));
</script>

<div
	class="relative min-w-40 rounded-xl border-2 px-3 py-2.5 text-left shadow-sm transition-colors
	{data.stepType === 'condition' ? 'pr-12' : ''}
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
	{#if data.status}
		<div class="text-foreground mt-1 text-[10px] font-semibold tracking-wide uppercase">
			{data.status}
		</div>
	{/if}
	{#if data.stepType === 'condition'}
		<!-- Two outputs: the run takes the Then or Else path; the other is skipped. -->
		<span
			class="pointer-events-none absolute right-2 -translate-y-1/2 text-[9px] font-semibold tracking-wide text-emerald-600 dark:text-emerald-400"
			style="top: 35%"
		>
			then
		</span>
		<Handle id="then" type="source" position={Position.Right} style="top: 35%" class="!h-2.5 !w-2.5 !bg-emerald-500" />
		<span
			class="text-muted-foreground pointer-events-none absolute right-2 -translate-y-1/2 text-[9px] font-semibold tracking-wide"
			style="top: 65%"
		>
			else
		</span>
		<Handle id="else" type="source" position={Position.Right} style="top: 65%" class="!bg-muted-foreground !h-2.5 !w-2.5" />
	{:else}
		<Handle type="source" position={Position.Right} class="!bg-muted-foreground !h-2.5 !w-2.5" />
	{/if}
</div>
