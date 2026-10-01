<script lang="ts">
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';

	let { location }: { location: Location } = $props();
</script>

<p class="tags">
	{#if location.isSupplier}<span class="tag supplier">Supplier</span>{/if}
	{#each location.categories as category (category.id)}
		<span class="tag">{category.name}</span>
	{/each}
	{#if location.archivedAt}<span class="tag archived">Archived</span>{/if}
</p>

<style>
	.tags {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
		margin: 0;
	}

	/* Static labels styled like the shared .role-chip, not interactive m3 Chips. */
	.tag {
		padding: 0.2rem 0.65rem;
		border-radius: 999px;
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
		font-size: 0.75rem;
		font-weight: 650;
	}

	.supplier {
		background: var(--m3c-primary-container);
		color: var(--m3c-on-primary-container);
	}

	.archived {
		background: var(--m3c-tertiary-container);
		color: var(--m3c-on-tertiary-container);
	}
</style>
