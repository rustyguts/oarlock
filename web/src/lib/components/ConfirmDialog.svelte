<script lang="ts">
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { buttonVariants } from '$lib/components/ui/button/index.js';
	import { cn } from '$lib/utils.js';

	let {
		open = $bindable(false),
		title,
		description,
		confirmText = 'Delete',
		cancelText = 'Cancel',
		destructive = true,
		onconfirm
	}: {
		open?: boolean;
		title: string;
		description?: string;
		confirmText?: string;
		cancelText?: string;
		destructive?: boolean;
		onconfirm: () => void;
	} = $props();

	let confirmBtn = $state<HTMLButtonElement | null>(null);

	function confirm(e: SubmitEvent) {
		e.preventDefault();
		open = false;
		onconfirm();
	}
</script>

<AlertDialog.Root bind:open>
	<AlertDialog.Content
		onOpenAutoFocus={(e: Event) => {
			// Focus the confirm button so Enter submits the wrapping form.
			e.preventDefault();
			confirmBtn?.focus();
		}}
	>
		<form onsubmit={confirm}>
			<AlertDialog.Header>
				<AlertDialog.Title>{title}</AlertDialog.Title>
				{#if description}
					<AlertDialog.Description>{description}</AlertDialog.Description>
				{/if}
			</AlertDialog.Header>
			<AlertDialog.Footer class="mt-4">
				<AlertDialog.Cancel type="button">{cancelText}</AlertDialog.Cancel>
				<button
					bind:this={confirmBtn}
					type="submit"
					class={cn(buttonVariants({ variant: destructive ? 'destructive' : 'default' }))}
				>
					{confirmText}
				</button>
			</AlertDialog.Footer>
		</form>
	</AlertDialog.Content>
</AlertDialog.Root>
