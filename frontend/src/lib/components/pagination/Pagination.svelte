<script lang="ts">
	import { Button } from 'm3-svelte';
	let {
		page,
		totalPages,
		loading = false,
		onchange
	}: {
		page: number;
		totalPages: number;
		loading?: boolean;
		onchange: (page: number) => void;
	} = $props();
</script>

{#if totalPages > 1 || page > 1}
	<nav class="pagination" aria-label="Results pages">
		<Button variant="outlined" disabled={loading || page <= 1} onclick={() => onchange(page - 1)}
			>Previous</Button
		>
		<span>Page {page}{totalPages ? ` of ${totalPages}` : ''}</span>
		<Button
			variant="outlined"
			disabled={loading || page >= totalPages}
			onclick={() => onchange(page + 1)}>Next</Button
		>
	</nav>
{/if}
