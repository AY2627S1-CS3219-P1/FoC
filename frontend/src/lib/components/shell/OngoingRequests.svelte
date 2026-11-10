<script lang="ts">
	import { page } from '$app/state';
	import { Icon, LoadingIndicator } from 'm3-svelte';
	import iconBag from '@ktibow/iconset-material-symbols/shopping-bag-outline';
	import iconBike from '@ktibow/iconset-material-symbols/pedal-bike';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import { authService, orderApi, realtimeService } from '$lib/services';
	import { statusLabel } from '$lib/services/order/presentation';


	let orders = $state<Order[] | null>(null);
	let hasMore = $state(false);
	let failed = $state(false);
	let request = 0;
 let loadedCaller: string | undefined;

	// Navigation, push, and fallback share the same mounted read.
	$effect(() => {
		const caller = authService.user?.id;
        if (loadedCaller !== caller) { orders = null; loadedCaller = caller; }
		void page.url.pathname;
		if (!caller) { orders = null; return; }
		return realtimeService.subscribe(['orders'], () => load(caller));
	});

	async function load(caller: string | undefined) {
		const current = ++request;
		if (!caller) {
			orders = null;
			return;
		}
		try {
			const result = await orderApi.listMyOrders({ activeOnly: true, pageSize: 20 });
			if (current === request && authService.user?.id === caller) {
				// active_only already excludes ended orders and ones the caller withdrew from.
				orders = result.orders;
				hasMore = result.totalPages > 1;
				failed = false;
			}
		} catch {
			if (current === request && authService.user?.id === caller) failed = true;
		}
	}

	function isCourier(order: Order): boolean {
		return order.requesterId !== authService.user?.id;
	}

	// Say what happens next from the caller's side, not the raw status.
	function nextStep(order: Order, courier: boolean): string {
		switch (order.status) {
			case OrderStatus.PENDING_RESERVATION:
				return 'Reserving credits';
			case OrderStatus.AWAITING_COURIER:
				return 'Waiting for a courier';
			case OrderStatus.AWAITING_PICKUP:
				return courier ? 'Pick it up' : 'Courier assigned';
			case OrderStatus.DELIVERING:
				return courier ? 'Deliver it' : 'On the way';
			case OrderStatus.DELIVERED:
				return courier ? 'Waiting for confirmation' : 'Confirm you received it';
			default:
				return statusLabel(order.status);
		}
	}
</script>

<section class="ongoing" aria-labelledby="ongoing-title">
	<h2 id="ongoing-title">Ongoing</h2>
	{#if failed && !orders}
		<p class="hint" role="alert">Couldn't load your requests. They'll retry shortly.</p>
	{:else if !orders}
		<div class="loading"><LoadingIndicator size={24} aria-label="Loading ongoing requests" /></div>
	{:else if !orders.length}
		<p class="hint">Nothing in progress.</p>
	{:else}
		<ul>
			{#each orders as order (order.id)}
				{@const courier = isCourier(order)}
				<li>
					<a
						class="request"
						href={'/orders/' + encodeURIComponent(order.id)}
						aria-current={page.url.pathname === '/orders/' + order.id ? 'page' : undefined}
					>
						<span class="role" class:courier title={courier ? "You're delivering" : 'You requested'}>
							<Icon icon={courier ? iconBike : iconBag} size={18} />
							<span class="visually-hidden">{courier ? "You're delivering:" : 'You requested:'}</span>
						</span>
						<span class="text">
							<span class="item">{order.itemDescription}</span>
							<span class="step">{nextStep(order, courier)}</span>
						</span>
					</a>
				</li>
			{/each}
		</ul>
		{#if failed}<p class="hint" role="alert">This list may be out of date.</p>{/if}
	{/if}
	{#if hasMore}<a class="more" href="/orders/mine">See all your orders</a>{/if}
</section>

<style>
	.ongoing {
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		align-content: start;
		gap: 0.25rem;
		min-height: 0;
		padding-top: 0.75rem;
		border-top: 1px solid var(--m3c-outline-variant);
	}

	h2 {
		margin: 0;
		padding: 0.25rem 1rem;
		font-size: 0.875rem;
		font-weight: 600;
		color: var(--m3c-on-surface-variant);
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		align-content: start;
		gap: 0.15rem;
		overflow-y: auto;
	}

	/* Rows share the nav items' shape and selected state. */
	.request {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.5rem 1rem;
		border-radius: var(--m3-shape-full, 999px);
		color: var(--m3c-on-surface);
		text-decoration: none;
	}

	.request:hover {
		background: color-mix(in srgb, var(--m3c-on-surface) 8%, transparent);
	}

	.request:focus-visible {
		outline: 2px solid var(--m3c-primary);
		outline-offset: -2px;
	}

	.request[aria-current='page'] {
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
	}

	/* The role is shown by icon and tint, and named for screen readers. */
	.role {
		flex: 0 0 auto;
		display: grid;
		place-items: center;
		width: 1.75rem;
		height: 1.75rem;
		border-radius: 50%;
		background: var(--m3c-primary-container);
		color: var(--m3c-on-primary-container);
	}

	.role.courier {
		background: var(--m3c-tertiary-container);
		color: var(--m3c-on-tertiary-container);
	}

	.text {
		display: grid;
		min-width: 0;
	}

	.item,
	.step {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.item {
		font-size: 0.9rem;
		font-weight: 500;
	}

	.step {
		font-size: 0.8rem;
		color: var(--m3c-on-surface-variant);
	}

	.more {
		padding: 0.25rem 1rem;
		font-size: 0.85rem;
	}

	.hint {
		margin: 0;
		padding: 0.25rem 1rem;
		font-size: 0.85rem;
		line-height: 1.4;
		color: var(--m3c-on-surface-variant);
	}

	.loading {
		display: flex;
		justify-content: center;
		padding: 0.5rem;
	}

	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}
</style>
