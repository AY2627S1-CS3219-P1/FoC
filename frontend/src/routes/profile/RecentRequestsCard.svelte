<script lang="ts">
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, orderApi, reviewApi, realtimeService } from '$lib/services';
	import { describeOrderError } from '$lib/services/order/errors';
	import { participationLabel, statusLabel } from '$lib/services/order/presentation';

	const shown = 5;
	let orders = $state<Order[] | null>(null);
	let more = $state(false);
	let error = $state('');
	let revision = $state(0);
	// Removed reviews get a notice rather than an action that cannot succeed.
	let reviewedIds = $state<Set<string>>(new Set());
	let removedIds = $state<Set<string>>(new Set());
	let reviewStatusesLoaded = $state(false);
	let loadedCaller: string | undefined;

	$effect(() => {
		const caller = authService.user?.id;
		if (loadedCaller !== caller) {
			orders = null;
			loadedCaller = caller;
		}
		void revision;
		reviewedIds = new Set();
		removedIds = new Set();
		reviewStatusesLoaded = false;
		if (!caller) return;
		let active = true;
		error = '';
		const stop = realtimeService.subscribe(['orders'], () => orderApi
			.listMyOrders({ pageSize: shown })
			.then((page) => {
				if (!active) return;
				orders = page.orders;
				error = '';
				more = page.totalPages > 1;
				reviewedIds = new Set();
				removedIds = new Set();
				reviewStatusesLoaded = false;
				const eligible = page.orders
					.filter(
						(order) =>
							order.status === OrderStatus.COMPLETED &&
							(order.requesterId === caller || order.courierId === caller)
					)
					.map((order) => order.id);
				if (eligible.length === 0) {
					reviewStatusesLoaded = true;
					return;
				}
				return reviewApi
					.listReviewOrderStatuses(eligible, AbortSignal.timeout(15000))
					.then((statuses) => {
						if (!active) return;
						reviewedIds = new Set(statuses.reviewedOrderIds);
						removedIds = new Set(statuses.removedOrderIds);
						reviewStatusesLoaded = true;
					})
					.catch(() => {
						if (active) reviewStatusesLoaded = false;
					});
			})
			.catch((cause) => active && (error = describeOrderError(cause))));
		return () => { active = false; stop(); };
	});

	const date = new Intl.DateTimeFormat('en-SG', { day: 'numeric', month: 'short', timeZone: 'Asia/Singapore' });
	const role = (order: Order) => participationLabel(order, authService.user?.id);
	const reviewable = (order: Order) =>
		order.status === OrderStatus.COMPLETED &&
		(order.requesterId === authService.user?.id || order.courierId === authService.user?.id);
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
		{/if}
		{#if !orders}
			{#if !error}<div class="loading-state"><LoadingIndicator aria-label="Loading request history" /></div>{/if}
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
						{#if reviewable(order) && reviewStatusesLoaded}
							{#if removedIds.has(order.id)}
								<span class="meta">Review removed</span>
							{:else}
								<Button
									variant="text"
									size="s"
									href={`/orders/${order.id}#review`}
									aria-label={`${reviewedIds.has(order.id) ? 'View your review of' : 'Leave a review for'} ${order.itemDescription}`}
								>{reviewedIds.has(order.id) ? 'View review' : 'Leave a review'}</Button>
							{/if}
						{/if}
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
	li { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; column-gap: 0.5rem; border-bottom: 1px solid var(--m3c-outline-variant); }
	li:last-child { border-bottom: 0; }
	a { display: grid; gap: 0.1rem; padding: 0.6rem 0; color: inherit; text-decoration: none; min-width: 0; }
	a:hover .title { text-decoration: underline; }
	a:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: 2px; }
	.title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
	.meta { font-size: 0.85rem; color: var(--m3c-on-surface-variant); }
	li :global(button) { height: auto; min-height: 2.25rem; padding-block: 0.35rem; }
</style>
