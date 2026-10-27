<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { Button, Card, LoadingIndicator, SelectOutlined } from 'm3-svelte';
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
		pickup = query.pickupLocationId ?? '';
	});

	$effect(() => {
		const user = authService.user?.id;
		if (!history && user && !suspended) {
			let active = true;
			locationApi.listBuildings().then((values) => { if (active) buildings = values; },
				() => { if (active) buildingError = 'Destination filters unavailable. Reload to try again.'; });
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
		<div><h1 id="orders-title">{history ? 'My Orders' : 'Browse orders'}</h1></div>
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
				<SelectOutlined label="Role" width="100%" value={page.url.searchParams.get('role') ?? ''} options={[{value: '', text: 'All roles'}, {value: 'requester', text: 'Requester'}, {value: 'courier', text: 'Courier'}]} onchange={(e) => update('role', e.currentTarget.value)} />
				<SelectOutlined label="Order status" width="100%" value={page.url.searchParams.get('status') ?? ''} options={[{value: '', text: 'All statuses'}, ...orderStatuses.map(([value, text]) => ({value: String(value), text}))]} onchange={(e) => update('status', e.currentTarget.value)} />
			{:else}
				<LocationPicker label="Pickup location" bind:value={pickup} onchange={(location) => update('pickup', location.id)} />
				<SelectOutlined label="Destination" width="100%" value={query.deliveryBuildingName ?? ''} options={[{value: '', text: 'All destination areas'}, ...buildings.map((building) => ({value: building.name, text: building.name}))]} onchange={(e) => update('destination', e.currentTarget.value)} />
				{#if buildingError}<p class="notice error" role="alert">{buildingError}</p>{/if}
			{/if}
			<a href={page.url.pathname}>Clear filters</a>
		</div></Card>
		{#if error}<p class="notice error" role="alert">{error}</p><Button onclick={() => revision++}>Retry</Button>
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
	.cards { display: grid; gap: 1rem; grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr)); list-style: none; padding: 0; margin: 0; }
	li { min-width: 0; }
</style>
