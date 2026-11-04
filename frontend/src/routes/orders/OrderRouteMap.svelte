<script lang="ts">
	import CampusMap, { type MapMarker } from '$lib/components/map/CampusMap.svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import type { Building, Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { locationApi } from '$lib/services';

	let { order, buildings }: { order: Order; buildings: Building[] } = $props();

	// Orders don't carry coordinates yet (#235), so read the pickup's from Supplier.
	let pickup = $state<Location | null>(null);
	$effect(() => {
		const id = order.pickup?.locationId;
		let stale = false;
		pickup = null;
		if (!id) return;
		locationApi.getLocation(id).then(
			(found) => !stale && (pickup = found),
			() => {}
		);
		return () => (stale = true);
	});

	const markers = $derived.by<MapMarker[]>(() => {
		const out: MapMarker[] = [];
		if (pickup?.coordinates) {
			out.push({
				id: 'pickup',
				latitude: pickup.coordinates.latitude,
				longitude: pickup.coordinates.longitude,
				label: 'Pick up: ' + (order.pickup?.name ?? ''),
				kind: 'pickup'
			});
		}
		const center = buildings.find((building) => building.name === order.deliveryBuildingName)?.center;
		if (center) {
			out.push({
				id: 'destination',
				latitude: center.latitude,
				longitude: center.longitude,
				label: 'Deliver to: ' + order.deliveryBuildingName,
				kind: 'destination'
			});
		}
		return out;
	});
</script>

<div class="route-map">
	<CampusMap {markers} route={['pickup', 'destination']} label="Map from pickup to destination" />
	<p class="legend"><span class="dot pickup"></span>Pick up <span class="dot destination"></span>Deliver to</p>
</div>

<style>
	.route-map {
		position: relative;
		height: 100%;
		min-height: 12rem;
	}

	.legend {
		position: absolute;
		left: 0.5rem;
		bottom: 0.5rem;
		z-index: 500;
		display: flex;
		align-items: center;
		gap: 0.4rem;
		margin: 0;
		padding: 0.3rem 0.65rem;
		border-radius: var(--m3-shape-full, 999px);
		background: var(--m3c-surface-container-lowest, #fff);
		font-size: 0.8rem;
		box-shadow: 0 1px 4px rgb(0 0 0 / 0.25);
	}

	.dot {
		display: inline-block;
		width: 0.65rem;
		height: 0.65rem;
		border-radius: 50%;
	}

	.dot.pickup {
		background: var(--m3c-tertiary);
	}

	.dot.destination {
		margin-inline-start: 0.4rem;
		background: var(--m3c-error);
	}
</style>
