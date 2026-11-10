<script lang="ts">
	import { Button, Dialog } from 'm3-svelte';
	import type { Snippet } from 'svelte';
	let {
		open = $bindable(false),
		title,
		label = 'Confirm',
		busy = false,
		danger = false,
		onconfirm,
		children
	}: {
		open?: boolean;
		title: string;
		label?: string;
		busy?: boolean;
		/** Style the confirm action as destructive. */
		danger?: boolean;
		onconfirm: () => void;
		children: Snippet;
	} = $props();
</script>

{#snippet buttons()}
	<Button type="button" variant="text" disabled={busy} onclick={() => (open = false)}>Cancel</Button
	>
	<Button
		type="button"
		disabled={busy}
		style={danger ? '--m3c-primary: var(--m3c-error); --m3c-on-primary: #fff;' : undefined}
		onclick={onconfirm}>{busy ? 'Working…' : label}</Button
	>
{/snippet}
<Dialog
	bind:open
	headline={title}
	aria-label={title}
	closedby={busy ? 'none' : 'any'}
	oncancel={(event) => {
		if (busy) event.preventDefault();
	}}
	{buttons}>{@render children()}</Dialog
>
