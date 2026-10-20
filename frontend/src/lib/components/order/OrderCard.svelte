<script lang="ts">
	import { Card } from 'm3-svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import { statusLabel } from '$lib/services/order/presentation';
	let { order }: { order: Order } = $props();
</script>

<Card variant="outlined">
	<article>
		<p class="status">{statusLabel(order.status)}</p>
		<h2><a href={'/orders/' + encodeURIComponent(order.id)}>{order.itemDescription}</a></h2>
		<p>Pick up: {order.pickup?.name ?? 'Unavailable'} · {order.pickup?.buildingName ?? ''}</p>
		<p>Deliver to: {order.deliveryBuildingName || 'Area unavailable'}</p>
		<p>{order.rewardCredits.toString()} credits</p>
		<small>Requester: {order.requesterId}</small>
		{#if order.courierId}<small>Current courier: {order.courierId}</small>{/if}
	</article>
</Card>

<style>
	article { display: grid; gap: .6rem; padding: 1rem; overflow-wrap: anywhere; }
	p, h2 { margin: 0; }
	h2 { font-size: 1.15rem; }
	.status { font-weight: 650; color: var(--m3c-primary); }
</style>
