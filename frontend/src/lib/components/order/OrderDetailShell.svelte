<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Card } from 'm3-svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import { dateLabel, statusLabel } from '$lib/services/order/presentation';

	let { order, refresh, actions }: {
		order: Order;
		refresh: () => Promise<void>;
		actions?: Snippet<[Order, () => Promise<void>]>;
	} = $props();
</script>

<Card variant="outlined">
	<div class="panel-body">
		<p class="eyebrow">{statusLabel(order.status)}</p>
		<h1>{order.itemDescription}</h1>
		<dl>
			<dt>Pickup</dt><dd>{order.pickup?.name} · {order.pickup?.buildingName}{order.pickup?.floor ? ' · ' + order.pickup.floor : ''}</dd>
			<dt>Destination area</dt><dd>{order.deliveryBuildingName || 'Area unavailable'}</dd>
			{#if order.delivery?.location?.locationId}
				<dt>Exact destination</dt><dd>{order.delivery.location.name}{order.delivery.location.floor ? ' · ' + order.delivery.location.floor : ''}</dd>
			{:else}<dt>Destination access</dt><dd>Exact destination is restricted to authorized participants.</dd>{/if}
			{#if order.delivery?.instructions}<dt>Delivery instructions</dt><dd>{order.delivery.instructions}</dd>{/if}
			<dt>Reward</dt><dd>{order.rewardCredits.toString()} credits</dd>
			<dt>Allowed delivery time from pickup</dt><dd>{Number(order.deliveryDuration?.seconds ?? 0n) / 60} minutes</dd>
			<dt>Unaccepted order expires</dt><dd>{dateLabel(order.expiresAt)}</dd>
			<dt>Requester</dt><dd>{order.requesterId}</dd>
			<dt>Current courier</dt><dd>{order.courierId || 'Not assigned'}</dd>
			<dt>Created</dt><dd>{dateLabel(order.createdAt)}</dd>
			<dt>Last updated</dt><dd>{dateLabel(order.updatedAt)}</dd>
		</dl>
		{#if order.itemImages.length}
			<section aria-label="Item images" class="images">
				{#each order.itemImages as image (image.id)}
					<a href={image.downloadUrl} target="_blank" rel="noreferrer"><img src={image.downloadUrl} alt="Item description attachment" loading="lazy" /></a>
				{/each}
			</section>
		{/if}
		{#if actions}<section aria-label="Order actions">{@render actions(order, refresh)}</section>{/if}
	</div>
</Card>

<style>
	h1 { margin: 0; font-size: 1.6rem; overflow-wrap: anywhere; }
	dl { display: grid; grid-template-columns: minmax(8rem, 1fr) minmax(0, 2fr); gap: .6rem 1rem; margin: 0; }
	dt { font-weight: 650; } dd { margin: 0; overflow-wrap: anywhere; white-space: pre-wrap; }
	.images { display: flex; flex-wrap: wrap; gap: .5rem; }
	img { width: 9rem; height: 9rem; object-fit: contain; max-width: 100%; }
	@media (max-width: 36rem) { dl { grid-template-columns: 1fr; } dd { margin-bottom: .6rem; } }
</style>
