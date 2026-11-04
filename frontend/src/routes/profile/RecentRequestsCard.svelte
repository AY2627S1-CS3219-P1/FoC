<script lang="ts">
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import { authService, orderApi } from '$lib/services';
	import { describeOrderError } from '$lib/services/order/errors';
	import { statusLabel } from '$lib/services/order/presentation';

	const shown = 5;
	let orders = $state<Order[] | null>(null);
	let more = $state(false);
	let error = $state('');
	let revision = $state(0);

	$effect(() => {
		const caller = authService.user?.id;
		void revision;
		if (!caller) return;
		let active = true;
		error = '';
		orderApi
			.listMyOrders({ pageSize: shown })
			.then((page) => {
				if (!active) return;
				orders = page.orders;
				more = page.totalPages > 1;
			})
			.catch((cause) => active && (error = describeOrderError(cause)));
		return () => (active = false);
	});

	const date = new Intl.DateTimeFormat('en-SG', { day: 'numeric', month: 'short', timeZone: 'Asia/Singapore' });
	const role = (order: Order) => (order.requesterId === authService.user?.id ? 'Requested' : 'Delivered');
</script>

<Card variant="outlined">
	<section class="panel-body" aria-labelledby="history-title">
		<header class="head">
			<h2 id="history-title">Request history</h2>
			{#if more}<Button variant="text" size="s" href="/orders/mine?view=all">See all</Button>{/if}
		</header>
		{#if error}
			<p class="notice error" role="alert">{error}</p>
			<div><Button variant="outlined" onclick={() => revision++}>Try again</Button></div>
		{:else if !orders}
			<div class="loading-state"><LoadingIndicator aria-label="Loading request history" /></div>
		{:else if !orders.length}
			<p class="muted">No requests yet. <a href="/orders/new">Make a request</a> or <a href="/orders">deliver one</a>.</p>
		{:else}
			<ul>
				{#each orders as order (order.id)}
					<li>
						<a href={'/orders/' + encodeURIComponent(order.id)}>
							<span class="title">{order.itemDescription}</span>
							<span class="meta">{role(order)}, {statusLabel(order.status).toLowerCase()}{order.createdAt ? ', ' + date.format(timestampDate(order.createdAt)) : ''}</span>
						</a>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</Card>

<style>
	.head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
	h2 { margin: 0; font-size: 1.25rem; }
	.muted { margin: 0; color: var(--m3c-on-surface-variant); }
	ul { display: grid; margin: 0; padding: 0; list-style: none; }
	li { border-bottom: 1px solid var(--m3c-outline-variant); }
	li:last-child { border-bottom: 0; }
	a { display: grid; gap: 0.1rem; padding: 0.6rem 0; color: inherit; text-decoration: none; }
	a:hover .title { text-decoration: underline; }
	a:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: 2px; }
	.title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
	.meta { font-size: 0.85rem; color: var(--m3c-on-surface-variant); }
</style>
