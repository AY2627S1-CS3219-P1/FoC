<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Button, Dialog } from 'm3-svelte';

	let {
		open = $bindable(false),
		headline,
		children,
		confirmLabel = 'Confirm',
		cancelLabel = 'Cancel',
		pending = false,
		pendingLabel = 'Working…',
		destructive = false,
		onconfirm
	}: {
		open?: boolean;
		headline: string;
		children: Snippet;
		confirmLabel?: string;
		cancelLabel?: string;
		pending?: boolean;
		pendingLabel?: string;
		destructive?: boolean;
		onconfirm: () => void;
	} = $props();
</script>

{#snippet buttons()}
	<Button type="button" variant="text" disabled={pending} onclick={() => (open = false)}>{cancelLabel}</Button>
	<span class:destructive>
		<Button type="button" disabled={pending} onclick={() => { if (!pending) onconfirm(); }}>{pending ? pendingLabel : confirmLabel}</Button>
	</span>
{/snippet}

<Dialog
	bind:open
	{headline}
	{buttons}
	aria-label={headline}
	aria-busy={pending}
	closedby={pending ? 'none' : 'any'}
	oncancel={(event) => { if (pending) event.preventDefault(); }}
>
	{@render children()}
</Dialog>

<style>
	.destructive {
		--m3c-primary: var(--m3c-error);
		--m3c-on-primary: var(--m3c-on-error);
	}
</style>
