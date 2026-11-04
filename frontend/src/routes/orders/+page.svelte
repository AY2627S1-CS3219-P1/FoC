<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Button, Card, LoadingIndicator, SelectOutlined } from 'm3-svelte';
	import LocationPicker from '$lib/components/order/LocationPicker.svelte';
	import OrderRouteMap from './OrderRouteMap.svelte';
	import OrderOfferControls from '$lib/components/order-offers/OrderOfferControls.svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import type { Building } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, locationApi, orderApi, orderOffersApi } from '$lib/services';
	import type { OrderPage, OrderQuery } from '$lib/services/order';
	import { describeOrderError } from '$lib/services/order/errors';
	import { statusLabel } from '$lib/services/order/presentation';

	const suspended = $derived(authService.user?.role === UserRole.SUSPENDED_USER);
	const selectedId = $derived(page.url.searchParams.get('selected') ?? '');
	const query: OrderQuery = $derived({
		page: Math.max(1, Number(page.url.searchParams.get('page')) || 1),
		pickupLocationId: page.url.searchParams.get('pickup') || undefined,
		deliveryBuildingName: page.url.searchParams.get('destination') || undefined
	});

	let result = $state<OrderPage | null>(null);
	let error = $state('');
	let loading = $state(false);
	// revision reloads the list and the selected request; listRevision only the list.
	let revision = $state(0);
	let listRevision = $state(0);
	let buildings = $state<Building[]>([]);
	let buildingError = $state('');
	let pickup = $state('');
	let filtersOpen = $state(false);
	let now = $state(Date.now());

	$effect(() => {
		pickup = query.pickupLocationId ?? '';
		if (query.pickupLocationId) filtersOpen = true;
	});

	$effect(() => {
		const timer = setInterval(() => (now = Date.now()), 15_000);
		return () => clearInterval(timer);
	});

	$effect(() => {
		if (!authService.user?.id || suspended) return;
		let active = true;
		locationApi.listBuildings().then(
			(values) => active && (buildings = values),
			() => active && (buildingError = 'Destination filters are unavailable. Reload to try again.')
		);
		return () => (active = false);
	});

	$effect(() => {
		const identity = authService.user?.id;
		const current = query;
		void revision;
		void listRevision;
		error = '';
		if (!identity || suspended) return;
		let active = true;
		loading = true;
		orderApi
			.listOpenOrders(current)
			.then((value) => active && (result = value))
			.catch((cause) => active && (error = describeOrderError(cause, 'load open requests')))
			.finally(() => active && (loading = false));
		return () => (active = false);
	});

	// A selected request may have left the open list (accepted, expired), so read it directly.
	// Only the id drives this effect; reading `selected` here would re-run it on every fetch.
	let selected = $state<Order | null>(null);
	let selectedError = $state('');
	$effect(() => {
		const id = selectedId;
		const user = authService.user?.id;
		void revision; // Refresh reloads the selected request with the list.
		selectedError = '';
		if (!id || !user) {
			selected = null;
			return;
		}
		const listed = untrack(() => result?.orders.find((order) => order.id === id));
		if (untrack(() => selected?.id) !== id) selected = listed ?? null;
		let stale = false;
		orderApi
			.getOrder(id)
			.then((order) => !stale && (selected = order))
			.catch((cause) => !stale && !untrack(() => selected) && (selectedError = describeOrderError(cause, 'load this request')));
		return () => (stale = true);
	});

	// If the direct read failed but the list now has the request, show the listed copy.
	$effect(() => {
		const listed = result?.orders.find((order) => order.id === selectedId);
		if (listed && untrack(() => !selected && selectedError)) {
			selected = listed;
			selectedError = '';
		}
	});

	async function refreshSelected() {
		if (!selectedId) return;
		try {
			selected = await orderApi.getOrder(selectedId);
		} catch (cause) {
			selectedError = describeOrderError(cause, 'refresh this request');
		}
		listRevision++;
	}

	function hrefWith(changes: Record<string, string | null>): string {
		const params = new URLSearchParams(page.url.searchParams);
		for (const [name, value] of Object.entries(changes)) {
			if (value) params.set(name, value);
			else params.delete(name);
		}
		return page.url.pathname + (params.size ? '?' + params : '');
	}

	function update(name: string, value: string) {
		const changes: Record<string, string | null> = { [name]: value || null, selected: null };
		if (name !== 'page') changes.page = null;
		void goto(hrefWith(changes), { keepFocus: true, noScroll: true });
	}

	function timeLeft(order: Order): string {
		if (!order.expiresAt) return '';
		const minutes = Math.round((timestampDate(order.expiresAt).getTime() - now) / 60_000);
		if (minutes <= 0) return 'Closing now';
		if (minutes < 60) return `${minutes} min left to accept`;
		const hours = Math.floor(minutes / 60);
		return `${hours} h ${minutes % 60} min left to accept`;
	}

	const deliveryMinutes = (order: Order) => Number(order.deliveryDuration?.seconds ?? 0n) / 60;
</script>

<svelte:head><title>Deliver a request · Friend on Campus</title></svelte:head>

<section class="split" class:has-selection={Boolean(selectedId)} aria-labelledby="orders-title">
	<div class="list-pane">
		<header class="heading">
			<h1 id="orders-title">Deliver a request</h1>
			<Button variant="text" size="s" onclick={() => revision++} disabled={loading}>Refresh</Button>
		</header>

		{#if !authService.user}
			<p class="notice"><a href="/login">Sign in</a> to see open requests.</p>
		{:else if suspended}
			<p class="notice">Your account is suspended, so you can't deliver requests. You can still read <a href="/orders/mine">your orders</a>.</p>
		{:else}
			<div class="filters">
				<SelectOutlined
					label="Destination"
					width="100%"
					value={query.deliveryBuildingName ?? ''}
					options={[
						{ value: '', text: 'Any destination' },
						...buildings.map((building) => ({ value: building.name, text: building.name }))
					]}
					onchange={(event) => update('destination', event.currentTarget.value)}
				/>
				{#if buildingError}<p class="notice error" role="alert">{buildingError}</p>{/if}
				<div class="filter-toggle">
					<Button variant="text" size="s" aria-expanded={filtersOpen} onclick={() => (filtersOpen = !filtersOpen)}>
						{filtersOpen ? 'Hide pickup filter' : 'Filter by pickup'}
					</Button>
				</div>
				{#if filtersOpen}
					<LocationPicker label="Pickup location" bind:value={pickup} onchange={(location) => update('pickup', location.id)} onclear={() => update('pickup', '')} />
				{/if}
				{#if query.pickupLocationId || query.deliveryBuildingName}
					<a href={page.url.pathname}>Clear filters</a>
				{/if}
			</div>

			<div class="results" aria-live="polite">
				{#if error}
					<p class="notice error" role="alert">{error}</p>
					<Button variant="outlined" onclick={() => revision++}>Try again</Button>
				{:else if loading && !result}
					<div class="loading-state"><LoadingIndicator aria-label="Loading open requests" /></div>
				{:else if result}
					<p class="summary">
						{result.totalItems.toString()} open {result.totalItems === 1n ? 'request' : 'requests'}
					</p>
					{#if !result.orders.length}<p class="muted">Nothing matches. Try another destination, or check back soon.</p>{/if}
					<ul class="rows">
						{#each result.orders as order (order.id)}
							<li>
								<a
									class="row"
									href={hrefWith({ selected: order.id })}
									aria-current={order.id === selectedId ? 'true' : undefined}
									data-sveltekit-noscroll
									data-sveltekit-keepfocus
								>
									<span class="title-line">
										<span class="title">{order.itemDescription}</span>
										<span class="reward">{order.rewardCredits.toString()} credits</span>
									</span>
									<span class="meta">From {order.pickup?.name ?? 'pickup'} to {order.deliveryBuildingName || 'an unlisted area'}</span>
									<span class="meta">{timeLeft(order)}</span>
								</a>
							</li>
						{/each}
					</ul>
					{#if result.totalPages > 1}
						<nav class="pagination" aria-label="Request pages">
							<Button variant="outlined" disabled={result.page <= 1} onclick={() => update('page', String(result!.page - 1))}>Previous</Button>
							<span>Page {result.page} of {result.totalPages}</span>
							<Button variant="outlined" disabled={result.page >= result.totalPages} onclick={() => update('page', String(result!.page + 1))}>Next</Button>
						</nav>
					{/if}
				{/if}
			</div>
		{/if}
	</div>

	<div class="detail-pane">
		<Card variant="outlined">
			<div class="panel-body">
				{#if selectedId}
					<div class="back"><Button variant="text" href={hrefWith({ selected: null })} data-sveltekit-noscroll>Back to list</Button></div>
					{#if selectedError && !selected}
						<p class="notice error" role="alert">{selectedError}</p>
					{:else if !selected}
						<div class="loading-state"><LoadingIndicator aria-label="Loading request" /></div>
					{:else}
						<article class="request" aria-labelledby="request-title">
							<header>
								<p class="status">{statusLabel(selected.status)}</p>
								<h2 id="request-title">{selected.itemDescription}</h2>
								<p class="muted">From {selected.pickup?.name ?? 'pickup'}{selected.pickup?.buildingName ? ', ' + selected.pickup.buildingName : ''} to {selected.deliveryBuildingName || 'an unlisted area'}</p>
							</header>

							<div class="facts">
								<div><span class="value">{selected.rewardCredits.toString()}</span><span class="label">credits reward</span></div>
								<div><span class="value">{deliveryMinutes(selected)}</span><span class="label">minutes to deliver after pickup</span></div>
							</div>
							{#if timeLeft(selected)}<p class="muted">{timeLeft(selected)}</p>{/if}

							<div class="map"><OrderRouteMap order={selected} {buildings} /></div>

							{#if selected.itemImages.length}
								<div class="images" aria-label="Item photos">
									{#each selected.itemImages as image (image.id)}
										<a href={image.downloadUrl} target="_blank" rel="noreferrer"><img src={image.downloadUrl} alt="Item" loading="lazy" /></a>
									{/each}
								</div>
							{/if}

							{#if selectedError}<p class="notice error" role="alert">{selectedError}</p>{/if}
							<OrderOfferControls
								order={selected}
								refresh={refreshSelected}
								api={orderOffersApi}
								callerId={authService.user?.id ?? ''}
								{suspended}
							/>
							<div class="full-link"><Button variant="text" href={'/orders/' + encodeURIComponent(selected.id)}>Open full request</Button></div>
						</article>
					{/if}
				{:else}
					<p class="muted">Choose a request to see the route and accept it or make a counteroffer.</p>
				{/if}
			</div>
		</Card>
	</div>
</section>

<style>
	/* Wireframe: open requests on the left, the selected request on the right. */
	.split {
		display: grid;
		grid-template-columns: minmax(18rem, 2fr) minmax(0, 3fr);
		gap: 1.5rem;
		align-items: start;
		max-width: 76rem;
		margin: 0 auto;
	}

	.list-pane,
	.results,
	.filters {
		display: grid;
		gap: 0.75rem;
		min-width: 0;
	}

	.filter-toggle {
		justify-self: start;
	}

	.heading {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	h1 {
		margin: 0;
		font-size: clamp(1.6rem, 3vw, 2rem);
	}

	.detail-pane {
		position: sticky;
		top: 1rem;
		min-width: 0;
	}

	.back {
		display: none;
	}

	.summary {
		margin: 0;
		font-weight: 600;
	}

	.muted {
		margin: 0;
		color: var(--m3c-on-surface-variant);
	}

	.rows {
		display: grid;
		gap: 0.25rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.row {
		display: grid;
		gap: 0.15rem;
		padding: 0.75rem 1rem;
		border-radius: var(--m3-shape-medium);
		color: inherit;
		text-decoration: none;
	}

	.row:hover {
		background: var(--m3c-surface-container);
	}

	.row:focus-visible {
		outline: 2px solid var(--m3c-primary);
		outline-offset: -2px;
	}

	.row[aria-current] {
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
	}

	.title-line {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
		min-width: 0;
	}

	.title {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 600;
	}

	.reward {
		flex: 0 0 auto;
		font-weight: 650;
	}

	.meta {
		font-size: 0.875rem;
		color: var(--m3c-on-surface-variant);
	}

	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 1rem;
	}

	.request {
		display: grid;
		gap: 1rem;
		min-width: 0;
	}

	.full-link {
		justify-self: start;
	}

	.request header {
		display: grid;
		gap: 0.3rem;
	}

	.status {
		margin: 0;
		font-weight: 650;
		color: var(--m3c-primary);
	}

	h2 {
		margin: 0;
		font-size: clamp(1.4rem, 3vw, 1.8rem);
		line-height: 1.2;
		overflow-wrap: anywhere;
	}

	.facts {
		display: flex;
		flex-wrap: wrap;
		gap: 1.5rem;
	}

	.facts > div {
		display: grid;
	}

	.value {
		font-size: 1.75rem;
		font-weight: 700;
		line-height: 1.1;
	}

	.label {
		font-size: 0.85rem;
		color: var(--m3c-on-surface-variant);
	}

	.map {
		height: 14rem;
	}

	.images {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	img {
		width: 6rem;
		height: 6rem;
		object-fit: cover;
		border-radius: var(--m3-shape-small, 8px);
	}

	@media (max-width: 60rem) {
		.split {
			grid-template-columns: minmax(0, 1fr);
		}

		.detail-pane {
			position: static;
			display: none;
		}

		.has-selection .detail-pane {
			display: block;
		}

		.has-selection .list-pane {
			display: none;
		}

		.back {
			display: block;
		}
	}
</style>
