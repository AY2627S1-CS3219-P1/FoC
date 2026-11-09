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
		onconfirm
	}: {
		open?: boolean;
		headline: string;
		children: Snippet;
		confirmLabel?: string;
		cancelLabel?: string;
		pending?: boolean;
		pendingLabel?: string;
		onconfirm: () => void;
	} = $props();
</script>

{#snippet buttons()}
	<Button type="button" variant="text" disabled={pending} onclick={() => (open = false)}>{cancelLabel}</Button>
	<Button type="button" disabled={pending} onclick={() => { if (!pending) onconfirm(); }}>{pending ? pendingLabel : confirmLabel}</Button>
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
