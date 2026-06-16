<script lang="ts">
	import type { StepType } from '$lib/api';
	import { stepIcon } from '$lib/step-icons';

	let { stepTypes }: { stepTypes: StepType[] } = $props();

	function ondragstart(e: DragEvent, type: string) {
		e.dataTransfer?.setData('application/oarlock-step', type);
		if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move';
	}
</script>

<aside class="bg-background w-64 shrink-0 overflow-y-auto border-r p-3 max-lg:hidden">
	<h2 class="text-muted-foreground/70 px-1 text-[11px] font-semibold tracking-widest uppercase">
		Steps
	</h2>
	<p class="text-muted-foreground/60 mt-1 mb-3 px-1 text-xs leading-relaxed">
		Drag a step onto the canvas
	</p>
	<div class="space-y-2">
		{#each stepTypes as st (st.type)}
			{@const Icon = stepIcon(st.type)}
			<div
				role="listitem"
				draggable="true"
				ondragstart={(e) => ondragstart(e, st.type)}
				class="group bg-card hover:border-primary/60 hover:shadow-sm flex cursor-grab items-start gap-3 rounded-lg border p-3 transition-all select-none active:cursor-grabbing"
			>
				<span
					class="bg-primary/12 text-primary-strong group-hover:bg-primary/20 flex size-9 shrink-0 items-center justify-center rounded-lg transition-colors"
				>
					<Icon class="size-4.5" />
				</span>
				<div class="min-w-0">
					<div class="text-[13px] leading-5 font-semibold tracking-tight">{st.label}</div>
					<div class="text-muted-foreground mt-0.5 line-clamp-2 text-xs leading-snug">
						{st.description}
					</div>
				</div>
			</div>
		{/each}
	</div>
</aside>
