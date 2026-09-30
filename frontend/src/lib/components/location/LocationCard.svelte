<script lang="ts">
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { formatOpeningHours } from '$lib/services/location/format';
	import LocationTags from './LocationTags.svelte';

	let { location }: { location: Location } = $props();
</script>

<a class="card" class:archived={location.archivedAt} href={`/suppliers/${location.id}`}>
	<h2>{location.name}</h2>
	<p class="muted">
		{location.building?.name}{#if location.floor}, floor {location.floor}{/if}
	</p>
	<LocationTags {location} />
	<p class="muted">{formatOpeningHours(location.opensAt, location.closesAt)}</p>
	{#if location.currentDisablement}
		<p class="closed">Temporarily closed: {location.currentDisablement.reason}</p>
	{/if}
</a>

<style>
	.card {
		display: block;
		height: 100%;
		padding: 1rem;
		border: 1px solid #dce3ee;
		border-radius: 0.6rem;
		background: #fff;
		color: inherit;
		text-decoration: none;
	}

	.card:hover {
		border-color: #174a99;
	}

	.archived {
		background: #f1f3f7;
	}

	h2 {
		margin: 0 0 0.25rem;
		font-size: 1.1rem;
	}

	p {
		margin: 0.35rem 0;
	}

	.muted {
		color: #527099;
		font-size: 0.9rem;
	}

	.closed {
		color: #8a4b00;
		font-size: 0.875rem;
		font-weight: 600;
	}
</style>
