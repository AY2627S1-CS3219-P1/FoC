<script lang="ts">
	import { Button, Card } from 'm3-svelte';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { formatOpeningHours } from '$lib/services/location/format';
	import LocationTags from './LocationTags.svelte';

	let { location, isAdmin = false }: { location: Location; isAdmin?: boolean } = $props();
</script>

<div class="location-card" class:archived={location.archivedAt}>
	<Card variant="outlined">
		<a class="card-link" href={`/suppliers/${location.id}`}>
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
		</a>
		{#if isAdmin}
			<div class="card-actions">
				<Button
					variant="outlined"
					href={`/admin/suppliers/${location.id}`}
					aria-label={`Edit ${location.name}`}>Edit</Button
				>
			</div>
		{/if}
	</Card>
</div>

<style>
	.location-card {
		height: 100%;
	}

	.location-card > :global(.m3-container) {
		height: 100%;
		transition: box-shadow var(--m3-easing-fast);
	}

	.location-card:hover > :global(.m3-container),
	.location-card:focus-within > :global(.m3-container) {
		box-shadow: var(--m3-elevation-1);
	}

	.archived > :global(.m3-container) {
		background: var(--m3c-surface-container);
	}

	.card-link {
		display: block;
		flex: 1;
		margin: -1rem;
		padding: 1rem;
		color: inherit;
		text-decoration: none;
		border-radius: var(--m3-shape-medium);
	}

	.card-actions {
		display: flex;
		justify-content: flex-end;
		margin-top: 1rem;
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
