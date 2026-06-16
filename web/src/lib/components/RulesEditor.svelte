<script lang="ts">
	import { Input } from '$lib/components/ui/input/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import XIcon from '@lucide/svelte/icons/x';

	interface Rule {
		operand?: string;
		operator?: string;
		value?: string;
		kind?: string;
	}

	let { rules = [], onchange }: { rules: Rule[]; onchange: (rules: Rule[]) => void } = $props();

	// Operators with no right-hand value.
	const VALUELESS = new Set(['exists', 'truthy']);
	const OPERATORS = ['==', '!=', '>', '<', '>=', '<=', 'contains', 'matches', 'exists', 'truthy'];
	const KINDS = ['string', 'number', 'boolean', 'expression'];

	// $state.raw in the parent: every change reassigns the whole array.
	function patch(i: number, p: Partial<Rule>) {
		onchange(rules.map((r, idx) => (idx === i ? { ...r, ...p } : r)));
	}
	function add() {
		onchange([...rules, { operand: '', operator: '==', value: '', kind: 'string' }]);
	}
	function remove(i: number) {
		onchange(rules.filter((_, idx) => idx !== i));
	}
</script>

<div class="mt-1 space-y-2">
	{#each rules as rule, i (i)}
		<div class="bg-muted/30 space-y-1.5 rounded-md border p-2">
			<div class="flex items-center gap-1.5">
				<Input
					placeholder="steps.fetch.body.count"
					value={rule.operand ?? ''}
					oninput={(e: Event) => patch(i, { operand: (e.target as HTMLInputElement).value })}
					class="h-7 flex-1 font-mono text-xs"
				/>
				<Button
					variant="ghost"
					size="icon"
					class="text-muted-foreground hover:text-destructive size-7 shrink-0"
					aria-label="Remove rule"
					onclick={() => remove(i)}
				>
					<XIcon class="size-3.5" />
				</Button>
			</div>
			<div class="flex items-center gap-1.5">
				<Select.Root
					type="single"
					value={rule.operator ?? '=='}
					onValueChange={(v: string) => patch(i, { operator: v })}
				>
					<Select.Trigger class="h-7 w-24 font-mono text-xs">{rule.operator ?? '=='}</Select.Trigger>
					<Select.Content>
						{#each OPERATORS as op (op)}
							<Select.Item value={op}>{op}</Select.Item>
						{/each}
					</Select.Content>
				</Select.Root>
				{#if !VALUELESS.has(rule.operator ?? '==')}
					<Input
						placeholder="value"
						value={rule.value ?? ''}
						oninput={(e: Event) => patch(i, { value: (e.target as HTMLInputElement).value })}
						class="h-7 flex-1 font-mono text-xs"
					/>
					<Select.Root
						type="single"
						value={rule.kind ?? 'string'}
						onValueChange={(v: string) => patch(i, { kind: v })}
					>
						<Select.Trigger class="h-7 w-24 text-xs">{rule.kind ?? 'string'}</Select.Trigger>
						<Select.Content>
							{#each KINDS as k (k)}
								<Select.Item value={k}>{k}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				{/if}
			</div>
		</div>
	{/each}
	<Button variant="outline" size="sm" class="w-full" onclick={add}>
		<PlusIcon class="size-3.5" /> Add rule
	</Button>
	{#if rules.length === 0}
		<p class="text-muted-foreground/70 text-[11px]">No rules yet — the condition is false (Else).</p>
	{/if}
</div>
