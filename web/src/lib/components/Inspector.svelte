<script lang="ts">
	import { api, type StepType, type Secret, type MCPServer } from '$lib/api';
	import type { StepNode } from '$lib/flow';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { Separator } from '$lib/components/ui/separator/index.js';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';

	let {
		node,
		stepTypes,
		secrets = [],
		mcpServers = [],
		onconfig,
		onretries,
		onrename,
		ondelete
	}: {
		node: StepNode;
		stepTypes: StepType[];
		secrets?: Secret[];
		mcpServers?: MCPServer[];
		onconfig: (id: string, config: Record<string, unknown>) => void;
		onretries: (id: string, retries: number) => void;
		onrename: (oldId: string, newId: string) => void;
		ondelete: (id: string) => void;
	} = $props();

	let spec = $derived(stepTypes.find((t) => t.type === node.data.stepType));
	let keyDraft = $state(node.id);
	$effect(() => {
		keyDraft = node.id;
	});

	// mcp_tool options depend on the selected server; fetched live.
	let toolOptions = $state<string[]>([]);
	let toolsError = $state('');
	$effect(() => {
		const serverName = String(node.data.config['server'] ?? '');
		toolOptions = [];
		toolsError = '';
		const srv = mcpServers.find((m) => m.name === serverName);
		if (!srv) return;
		api
			.mcpServerTools(srv.id)
			.then((tools) => (toolOptions = tools.map((t) => t.name)))
			.catch((e) => (toolsError = e instanceof Error ? e.message : String(e)));
	});

	function optionsFor(kind: string): string[] {
		if (kind === 'api_key') return secrets.filter((c) => c.type === 'api_key').map((c) => c.name);
		if (kind === 'mcp_server') return mcpServers.filter((m) => m.is_enabled).map((m) => m.name);
		if (kind === 'mcp_tool') return toolOptions;
		return [];
	}

	function emptyHintFor(kind: string): string {
		if (kind === 'api_key') return 'No API-key secrets yet — add one under Configuration.';
		if (kind === 'mcp_server') return 'No MCP servers yet — add one under MCP Servers.';
		if (kind === 'mcp_tool') return toolsError ? `Server unreachable: ${toolsError}` : 'Pick a server first.';
		return 'No options.';
	}

	function setConfig(key: string, value: unknown) {
		onconfig(node.id, { ...node.data.config, [key]: value });
	}

	function commitRename() {
		const next = keyDraft.trim().replace(/[^a-zA-Z0-9_-]+/g, '_');
		if (next && next !== node.id) onrename(node.id, next);
		else keyDraft = node.id;
	}
</script>

<aside class="bg-background flex w-72 shrink-0 flex-col overflow-y-auto border-l">
	<div class="p-3">
		<div class="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
			{spec?.label ?? node.data.stepType}
		</div>
		<label class="mt-2 block">
			<span class="text-muted-foreground text-xs">Step key</span>
			<Input
				bind:value={keyDraft}
				onblur={commitRename}
				onkeydown={(e: KeyboardEvent) =>
					e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
				class="mt-1 font-mono text-sm"
			/>
		</label>
	</div>
	<Separator />

	<div class="flex-1 space-y-3 p-3">
		{#each spec?.config_spec ?? [] as field (field.key)}
			<label class="block">
				<span class="text-muted-foreground text-xs">
					{field.label}{field.required ? ' *' : ''}
				</span>
				{#if field.kind === 'text'}
					<Textarea
						rows={4}
						placeholder={field.placeholder}
						value={String(node.data.config[field.key] ?? '')}
						oninput={(e: Event) => setConfig(field.key, (e.target as HTMLTextAreaElement).value)}
						class="mt-1 font-mono text-xs"
					/>
				{:else if field.kind === 'select'}
					<Select.Root
						type="single"
						value={String(node.data.config[field.key] ?? field.options?.[0] ?? '')}
						onValueChange={(v: string) => setConfig(field.key, v)}
					>
						<Select.Trigger class="mt-1 w-full">
							{String(node.data.config[field.key] ?? field.options?.[0] ?? '')}
						</Select.Trigger>
						<Select.Content>
							{#each field.options ?? [] as opt (opt)}
								<Select.Item value={opt}>{opt}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				{:else if field.kind === 'api_key' || field.kind === 'mcp_server' || field.kind === 'mcp_tool'}
					{@const opts = optionsFor(field.kind)}
					{#if opts.length === 0}
						<p class="text-muted-foreground bg-muted/50 mt-1 rounded-md border border-dashed px-2 py-1.5 text-xs">
							{emptyHintFor(field.kind)}
						</p>
					{:else}
						<Select.Root
							type="single"
							value={String(node.data.config[field.key] ?? '')}
							onValueChange={(v: string) => setConfig(field.key, v)}
						>
							<Select.Trigger class="mt-1 w-full">
								{String(node.data.config[field.key] ?? '') || `Select ${field.label.toLowerCase()}…`}
							</Select.Trigger>
							<Select.Content>
								{#each opts as opt (opt)}
									<Select.Item value={opt}>{opt}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
					{/if}
				{:else if field.kind === 'number'}
					<Input
						type="number"
						placeholder={field.placeholder}
						value={node.data.config[field.key] == null ? '' : String(node.data.config[field.key])}
						oninput={(e: Event) => {
							const raw = (e.target as HTMLInputElement).value;
							setConfig(field.key, raw === '' ? undefined : Number(raw));
						}}
						class="mt-1"
					/>
				{:else}
					<Input
						placeholder={field.placeholder}
						value={String(node.data.config[field.key] ?? '')}
						oninput={(e: Event) => setConfig(field.key, (e.target as HTMLInputElement).value)}
						class="mt-1"
					/>
				{/if}
			</label>
		{/each}

		<label class="block">
			<span class="text-muted-foreground text-xs">Retries on failure (0–10)</span>
			<Input
				type="number"
				min={0}
				max={10}
				value={node.data.retries == null ? '' : String(node.data.retries)}
				oninput={(e: Event) => {
					const raw = (e.target as HTMLInputElement).value;
					onretries(node.id, raw === '' ? 0 : Math.max(0, Math.min(10, Number(raw))));
				}}
				class="mt-1"
			/>
		</label>

		<p class="text-muted-foreground/70 text-[11px] leading-relaxed">
			Use <code class="bg-muted rounded px-1">{'{{ steps.<key> }}'}</code> and
			<code class="bg-muted rounded px-1">{'{{ input }}'}</code> expressions in string fields.
		</p>
	</div>

	<Separator />
	<div class="p-3">
		<Button variant="outline" class="text-destructive w-full" onclick={() => ondelete(node.id)}>
			<Trash2Icon class="size-4" /> Delete step
		</Button>
	</div>
</aside>
