<script lang="ts">
	import { Button } from 'm3-svelte';
	import type { Proximity } from './proximity.svelte';
	let {
		proximity,
		fallback = 'Showing newest requests and suppliers by name.'
	}: { proximity: Proximity; fallback?: string } = $props();
</script>

{#if proximity.locating}
	<p role="status">Finding your location…</p>
{:else if proximity.error}
	<div role="status">
		<span>{proximity.error} {fallback}</span>
		<Button variant="text" size="s" onclick={() => void proximity.locate()}>Retry location</Button>
	</div>
{/if}
