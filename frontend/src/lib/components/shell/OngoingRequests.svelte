<script lang="ts">
	import { page } from '$app/state';
	import { LoadingIndicator } from 'm3-svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import { authService, orderApi } from '$lib/services';
	import { statusLabel } from '$lib/services/order/presentation';

	const refreshMs = 30_000;

	let orders = $state<Order[] | null>(null);
	let failed = $state(false);
	let request = 0;

	// Reload on sign-in, on every navigation (an order page may have changed a
	// status), and periodically while the tab is visible.
	$effect(() => {
		const caller = authService.user?.id;
		void page.url.pathname;
		void load(caller);
	});

	$effect(() => {
		const caller = authService.user?.id;
		if (!caller) return;

		const timer = setInterval(() => {
			if (!document.hidden) void load(caller);
		}, refreshMs);
		const visible = () => {
			if (!document.hidden) void load(caller);
		};
		document.addEventListener('visibilitychange', visible);

		return () => {
			clearInterval(timer);
			document.removeEventListener('visibilitychange', visible);
		};
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
				orders = result.orders;
				failed = false;
			}
		} catch {
			if (current === request && authService.user?.id === caller) failed = true;
		}
	}

	function isCourier(order: Order): boolean {
		return order.requesterId !== authService.user?.id;
	}
</script>

<section class="ongoing" aria-labelledby="ongoing-title">
	<h2 id="ongoing-title">Ongoing requests</h2>
	{#if failed && !orders}
		<p class="hint" role="alert">Couldn't load your requests.</p>
	{:else if !orders}
		<div class="loading"><LoadingIndicator size={24} aria-label="Loading ongoing requests" /></div>
	{:else if !orders.length}
		<p class="hint">Nothing in progress. <a href="/orders/new">Make a request</a> or <a href="/orders">deliver one</a>.</p>
	{:else}
		<ul>
			{#each orders as order (order.id)}
				{@const courier = isCourier(order)}
				<li>
					<a
						class="request"
						class:courier
						href={'/orders/' + encodeURIComponent(order.id)}
						aria-current={page.url.pathname === '/orders/' + order.id ? 'page' : undefined}
					>
						<span class="role">{courier ? 'Delivering' : 'Requested'}</span>
						<span class="item">{order.itemDescription}</span>
						<span class="status">{statusLabel(order.status)}</span>
					</a>
				</li>
			{/each}
		</ul>
		{#if failed}<p class="hint" role="alert">This list may be out of date.</p>{/if}
	{/if}
</section>

<style>
	.ongoing {
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		align-content: start;
		gap: 0.5rem;
		min-height: 0;
	}

	h2 {
		margin: 0;
		padding: 0 0.75rem;
		font-size: 0.8rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--m3c-on-surface-variant);
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		align-content: start;
		gap: 0.4rem;
		overflow-y: auto;
	}

	/* Requester and courier roles differ by colour and by the label text. */
	.request {
		display: grid;
		gap: 0.1rem;
		padding: 0.55rem 0.75rem;
		border-radius: var(--m3-shape-medium);
		border-inline-start: 4px solid var(--m3c-primary);
		background: var(--m3c-primary-container);
		color: var(--m3c-on-primary-container);
		text-decoration: none;
	}

	.request.courier {
		border-inline-start-color: var(--m3c-tertiary);
		background: var(--m3c-tertiary-container);
		color: var(--m3c-on-tertiary-container);
	}

	.request[aria-current='page'] {
		outline: 2px solid currentColor;
		outline-offset: -2px;
	}

	.role {
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
	}

	.item {
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.status {
		font-size: 0.8rem;
	}

	.hint {
		margin: 0;
		padding: 0 0.75rem;
		font-size: 0.85rem;
		line-height: 1.4;
		color: var(--m3c-on-surface-variant);
	}

	.loading {
		display: flex;
		justify-content: center;
		padding: 0.5rem;
	}
</style>
