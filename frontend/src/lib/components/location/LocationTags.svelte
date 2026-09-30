<script lang="ts">
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';

	let { location }: { location: Location } = $props();
</script>

<p class="tags">
	{#if location.isSupplier}<span class="tag">Supplier</span>{/if}
	{#each location.categories as category (category.id)}
		<span class="tag subtle">{category.name}</span>
	{/each}
	{#if location.archivedAt}<span class="tag warn">Archived</span>{/if}
</p>

<style>
	.tags {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
		margin: 0.35rem 0;
	}

	.tag {
		padding: 0.1rem 0.5rem;
		border-radius: 999px;
		background: #174a99;
		color: #fff;
		font-size: 0.75rem;
		font-weight: 600;
	}

	.subtle {
		background: #e3ebf7;
		color: #17233b;
	}

	.warn {
		background: #fdecc8;
		color: #8a4b00;
	}
</style>
