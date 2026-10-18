<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, orderApi, locationApi } from '$lib/services';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
	import type { Building } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import type { OrderPage, OrderQuery } from '$lib/services/order';
	import { orderStatuses, roleFromQuery } from '$lib/services/order/presentation';
	import { describeOrderError } from '$lib/services/order/errors';
	import LocationPicker from './LocationPicker.svelte';
	import OrderCard from './OrderCard.svelte';

	let { history = false }: { history?: boolean } = $props();
	let result = $state<OrderPage | null>(null);
	let error = $state('');
	let loading = $state(false);
	let revision = $state(0);
	let buildings = $state<Building[]>([]);
	let buildingError = $state('');
	let pickup = $state('');
	const suspended = $derived(authService.user?.role === UserRole.SUSPENDED_USER);
	const query: OrderQuery = $derived({
		page: Math.max(1, Number(page.url.searchParams.get('page')) || 1),
		pickupLocationId: page.url.searchParams.get('pickup') || undefined,
		deliveryBuildingName: page.url.searchParams.get('destination') || undefined,
		role: roleFromQuery(page.url.searchParams.get('role')),
		status: Number(page.url.searchParams.get('status')) as OrderStatus || OrderStatus.UNSPECIFIED
	});

	$effect(() => {
		const user = authService.user?.id;
		if (!history && user && !suspended) {
			let active = true;
			locationApi.listBuildings().then((values) => { if (active) buildings = values; },
				() => { if (active) buildingError = 'Destination filters unavailable. Retry loading the page.'; });
			return () => { active = false; };
		}
	});

	$effect(() => {
		const identity = authService.user?.id;
		const current = query;
		void revision;
		result = null;
		error = '';
		if (!identity || (!history && suspended)) return;
		let active = true;
		loading = true;
		(history ? orderApi.listMyOrders(current) : orderApi.listOpenOrders(current))
			.then((value) => { if (active) result = value; })
			.catch((cause) => { if (active) error = describeOrderError(cause); })
			.finally(() => { if (active) loading = false; });
		return () => { active = false; };
	});

	function update(name: string, value: string) {
		const params = new URLSearchParams(page.url.searchParams);
		if (value) params.set(name, value); else params.delete(name);
		if (name !== 'page') params.delete('page');
		void goto(page.url.pathname + (params.size ? '?' + params : ''), { keepFocus: true, noScroll: true });
	}
</script>

<section class="page-content" aria-labelledby="orders-title">
	<header class="page-heading">
		<div><p class="eyebrow">Orders</p><h1 id="orders-title">{history ? 'My Orders' : 'Browse orders'}</h1>
			<p>{history ? 'Your requested deliveries and courier participation, including past assignments.' : 'Find an open request to deliver.'}</p></div>
	</header>
	<nav aria-label="Order navigation" class="links">
		<a href="/orders">Browse</a><a href="/orders/mine">My Orders</a>
		{#if !suspended}<a href="/orders/new">Create Order</a>{/if}
	</nav>
	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to see orders.</p>
	{:else if !history && suspended}<p class="notice">Your account is suspended. You can still read <a href="/orders/mine">My Orders</a>.</p>
	{:else}
		<Card variant="outlined"><div class="panel-body filters">
			{#if history}
				<label>Participation role<select value={page.url.searchParams.get('role') ?? ''} onchange={(e) => update('role', e.currentTarget.value)}><option value="">All roles</option><option value="requester">Requester</option><option value="courier">Courier</option></select></label>
				<label>Order status<select value={page.url.searchParams.get('status') ?? ''} onchange={(e) => update('status', e.currentTarget.value)}><option value="">All statuses</option>{#each orderStatuses as [value, label]}<option value={value}>{label}</option>{/each}</select></label>
			{:else}
				<LocationPicker label="Pickup location filter" bind:value={pickup} onchange={(location) => update('pickup', location.id)} />
				<label>Destination building/area<select value={query.deliveryBuildingName ?? ''} onchange={(e) => update('destination', e.currentTarget.value)}><option value="">All destination areas</option>{#each buildings as building (building.id)}<option value={building.name}>{building.name}</option>{/each}</select></label>
				{#if buildingError}<p class="notice error" role="alert">{buildingError}</p>{/if}
			{/if}
			<a href={page.url.pathname}>Clear filters</a>
		</div></Card>
		{#if error}<p class="notice error" role="alert">{error}</p><Button onclick={() => revision++}>Retry loading orders</Button>
		{:else if loading}<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading orders" /><p>Loading orders…</p></div>
		{:else if result}
			<p role="status">{result.totalItems.toString()} {result.totalItems === 1n ? 'order' : 'orders'}</p>
			{#if !result.orders.length}<p class="notice">No orders match these filters.</p>{/if}
			<ul class="cards">{#each result.orders as order (order.id)}<li><OrderCard {order} /></li>{/each}</ul>
			{#if result.totalPages > 1}<nav class="pagination" aria-label="Order pages">
				<Button variant="outlined" disabled={result.page <= 1} onclick={() => update('page', String(result!.page - 1))}>Previous</Button>
				<span>Page {result.page} of {result.totalPages}</span>
				<Button variant="outlined" disabled={result.page >= result.totalPages} onclick={() => update('page', String(result!.page + 1))}>Next</Button>
			</nav>{/if}
		{/if}
	{/if}
</section>

<style>
	.links, .pagination { display: flex; gap: 1rem; flex-wrap: wrap; align-items: center; }
	label { display: grid; gap: .5rem; }
	select { width: 100%; min-width: 0; padding: .75rem; font: inherit; background: var(--m3c-surface); color: var(--m3c-on-surface); border: 1px solid var(--m3c-outline); border-radius: 8px; }
	select:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	.cards { display: grid; gap: 1rem; grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr)); list-style: none; padding: 0; margin: 0; }
	li { min-width: 0; }
</style>
