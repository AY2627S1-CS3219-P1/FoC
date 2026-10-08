<script lang="ts">
	import { Card } from 'm3-svelte';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { formatOpeningHours } from '$lib/services/location/format';
	import LocationTags from './LocationTags.svelte';

	let { location }: { location: Location } = $props();
</script>

<a class="card-link" class:archived={location.archivedAt} href={`/suppliers/${location.id}`}>
	<Card variant="outlined">
		<div class="card-body">
			<h2>{location.name}</h2>
			<p class="muted">
				{location.building?.name}{#if location.floor}, floor {location.floor}{/if}
			</p>
			<LocationTags {location} />
			<p class="muted">{formatOpeningHours(location.opensAt, location.closesAt)}</p>
			{#if location.currentDisablement}
				<p class="closed">Temporarily closed: {location.currentDisablement.reason}</p>
			{/if}
		</div>
	</Card>
</a>

<style>
	.card-link {
		display: block;
		height: 100%;
		color: inherit;
		text-decoration: none;
		border-radius: var(--m3-shape-medium);
	}

	.card-link > :global(.m3-container) {
		height: 100%;
		transition: box-shadow var(--m3-easing-fast);
	}

	.card-link:hover > :global(.m3-container),
	.card-link:focus-visible > :global(.m3-container) {
		box-shadow: var(--m3-elevation-1);
	}

	.archived > :global(.m3-container) {
		background: var(--m3c-surface-container);
	}

	.card-body {
		display: grid;
		gap: 0.4rem;
	}

	h2 {
		margin: 0;
		font-size: 1.1rem;
	}

	p {
		margin: 0;
	}

	.muted {
		color: var(--m3c-on-surface-variant);
		font-size: 0.9rem;
	}

	.closed {
		color: var(--m3c-error);
		font-size: 0.875rem;
		font-weight: 600;
	}
</style>
