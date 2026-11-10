<script lang="ts">
	import { untrack } from 'svelte';
	import { Proximity } from '$lib/components/map/proximity.svelte';
	import LocationStatus from '$lib/components/map/LocationStatus.svelte';
	import { goto } from '$app/navigation';
	import { Button, Card, LoadingIndicator, SelectOutlined, TextFieldOutlined } from 'm3-svelte';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import { ParticipantRole } from '$lib/gen/order/v1/shared_types_pb';
	import type { Building, Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, locationApi, orderApi, realtimeService } from '$lib/services';
	import { describeOrderError } from '$lib/services/order/errors';
	import { describeLocationError } from '$lib/services/location/errors';
	import iconSearch from '@ktibow/iconset-material-symbols/search';
	import iconHourglass from '@ktibow/iconset-material-symbols/hourglass-empty';
	import CampusMap, { type MapMarker } from '$lib/components/map/CampusMap.svelte';
	import { distanceLabel, distanceMetres, type Point } from '$lib/components/map/geo';

	$effect(() => {
		if (!authService.isAuthenticated) void goto('/login', { replaceState: true });
	});

	type Mode = 'suppliers' | 'requests';
	type Selection = { kind: 'supplier' | 'request'; id: string } | null;

	let mode = $state<Mode>('suppliers');
	let selection = $state<Selection>(null);

	let suppliers = $state<Location[]>([]);
	let supplierError = $state('');
	let suppliersLoading = $state(true);
	let buildings = $state<Building[]>([]);
	let search = $state('');
	let searchResults = $state<Location[] | null>(null);
	let searching = $state(false);

	let openOrders = $state<Order[]>([]);
	let openError = $state('');
	let openLoading = $state(true);
	let recentPickupIds = $state<string[]>([]);

	// Recent pickups outside the supplier page are fetched on demand.
	let extraLocations = $state<Record<string, Location>>({});
	const proximity = new Proximity();
	const origin = $derived(proximity.origin);
	let requestSort = $state<'newest' | 'proximity'>('proximity');
	let supplierSort = $state<'name' | 'proximity'>('proximity');
	$effect(() => { if (authService.user?.id && !proximity.attempted) void untrack(() => proximity.locate()); });

	const suspended = $derived(authService.user?.role === UserRole.SUSPENDED_USER);

	// Recent history keeps its own order, independent of either proximity control.
	$effect(() => {
		if (!authService.user?.id) return;
		let active = true;
		locationApi.listBuildings().then((values) => { if (active) buildings = values; }, () => {});
		orderApi.listMyOrders({ role: ParticipantRole.REQUESTER, pageSize: 20 }).then((page) => {
			if (!active) return;
			const ids = page.orders.map((order) => order.pickup?.locationId).filter((id) => id !== undefined);
			recentPickupIds = [...new Set(ids)].slice(0, 4);
		}, () => {});
		return () => { active = false; };
	});

	$effect(() => {
		if (!authService.user?.id || !proximity.attempted || proximity.locating) return;
		if (suspended) { openLoading = false; return; }
		const query = { pageSize: 20, sort: origin ? requestSort : 'newest' as const, origin: origin ?? undefined };
		let active = true;
		openLoading = true;
		openError = '';
		const stop = realtimeService.subscribe(['openOrders'], () => orderApi.listOpenOrders(query)
			.then((page) => { if (active) { openOrders = page.orders; openError = ''; } })
			.catch((cause) => { if (active) openError = describeOrderError(cause, 'load open requests'); })
			.finally(() => { if (active) openLoading = false; }));
		return () => { active = false; stop(); };
	});

	let submittedSearch = $state('');
	let searchRevision = $state(0);
	$effect(() => {
		if (!authService.user?.id || !proximity.attempted || proximity.locating) return;
		const term = submittedSearch;
		void searchRevision;
		const query = { suppliersOnly: true, pageSize: term ? 20 : 100,
			search: term, sort: origin ? supplierSort : 'name' as const, origin: origin ?? undefined };
		let active = true;
		suppliersLoading = true;
		searching = Boolean(term);
		supplierError = '';
		locationApi.listLocations(query)
			.then((page) => {
				if (!active) return;
				if (term) {
					searchResults = page.locations;
					selection = page.locations[0] ? { kind: 'supplier', id: page.locations[0].id } : null;
				} else {
					suppliers = page.locations;
					searchResults = null;
				}
			})
			.catch((cause) => { if (active) supplierError = describeLocationError(cause); })
			.finally(() => { if (active) { suppliersLoading = false; searching = false; } });
		return () => { active = false; };
	});

	const locationsById = $derived(
		new Map([...suppliers, ...Object.values(extraLocations)].map((location) => [location.id, location]))
	);

	// Resolve recent pickups beyond the supplier page.
	$effect(() => {
		const known = locationsById;
		const ids = recentPickupIds;
		const missing = [...new Set(ids)].filter(
			(id): id is string => Boolean(id) && !known.has(id!)
		);
		if (!missing.length) return;
		let active = true;
		// Publish the batch together so each response doesn't restart other lookups.
		void Promise.allSettled(missing.map((id) => locationApi.getLocation(id))).then((results) => {
			if (!active) return;
			const found = results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []);
			if (found.length) {
				extraLocations = { ...extraLocations, ...Object.fromEntries(found.map((location) => [location.id, location])) };
			}
		});
		return () => { active = false; };
	});

	function pickupPoint(order: Order): Point | undefined {
		const coordinates = order.pickup?.coordinates;
		return coordinates ? { latitude: coordinates.latitude, longitude: coordinates.longitude } : undefined;
	}

	function destinationPoint(order: Order): Point | undefined {
		const center = buildings.find((building) => building.name === order.deliveryBuildingName)?.center;
		return center ? { latitude: center.latitude, longitude: center.longitude } : undefined;
	}

	const selectedOrder = $derived(
		selection?.kind === 'request' ? openOrders.find((order) => order.id === selection!.id) : undefined
	);

	const sortedOrders = $derived(openOrders);

	const visibleSuppliers = $derived(searchResults ?? suppliers);
	const recentSuppliers = $derived(
		recentPickupIds.map((id) => locationsById.get(id)).filter((location) => location !== undefined)
	);

	const markers = $derived.by<MapMarker[]>(() => {
		if (selectedOrder) {
			const pickup = pickupPoint(selectedOrder);
			const destination = destinationPoint(selectedOrder);
			return [
				...(pickup ? [{ id: 'pickup', ...pickup, kind: 'pickup' as const, label: 'Pick up: ' + (selectedOrder.pickup?.name ?? '') }] : []),
				...(destination ? [{ id: 'destination', ...destination, kind: 'destination' as const, label: 'Deliver to: ' + selectedOrder.deliveryBuildingName }] : [])
			];
		}
		if (mode === 'requests') {
			return sortedOrders.flatMap((order) => {
				const point = pickupPoint(order);
				return point ? [{ id: order.id, ...point, kind: 'pickup' as const, label: order.itemDescription }] : [];
			});
		}
		const displayed = searchResults ? visibleSuppliers : [...recentSuppliers, ...visibleSuppliers];
		return [...new Map(displayed.map((location) => [location.id, location])).values()].flatMap((location) =>
			location.coordinates
				? [{ id: location.id, latitude: location.coordinates.latitude, longitude: location.coordinates.longitude, kind: 'supplier' as const, label: location.name }]
				: []
		);
	});

	function selectSupplier(id: string) {
		mode = 'suppliers';
		selection = selection?.kind === 'supplier' && selection.id === id ? null : { kind: 'supplier', id };
	}

	function selectRequest(id: string) {
		mode = 'requests';
		selection = selection?.kind === 'request' && selection.id === id ? null : { kind: 'request', id };
	}

	function selectMarker(id: string) {
		if (mode === 'requests') selectRequest(id);
		else selectSupplier(id);
	}

	function setMode(next: Mode) {
		mode = next;
		selection = null;
	}

	function findSuppliers(event?: SubmitEvent) {
		event?.preventDefault();
		submittedSearch = search.trim();
		searchRevision++;
		mode = 'suppliers';
		selection = null;
	}

	function clearSearch() {
		search = '';
		submittedSearch = '';
		searchResults = null;
		selection = null;
	}

	function distanceTo(order: Order): string {
		const point = origin ? pickupPoint(order) : undefined;
		return origin && point ? distanceLabel(distanceMetres(origin, point)) : '';
	}
</script>

<svelte:head><title>Home · Friend on Campus</title></svelte:head>

{#snippet supplierItem(location: Location)}
	{@const selected = selection?.kind === 'supplier' && selection.id === location.id}
	<li class="row" class:selected>
		<button type="button" class="row-main" aria-pressed={selected} onclick={() => selectSupplier(location.id)}>
			<span class="title">{location.name}</span>
			<span class="meta">{location.building?.name ?? ''}{origin && location.coordinates ? ', ' + distanceLabel(distanceMetres(origin, location.coordinates)) + ' away' : ''}{location.floor ? ', ' + location.floor : ''}</span>
			{#if location.currentDisablement}<span class="closed">Closed now</span>{/if}
		</button>
		{#if location.archivedAt}
			<span class="closed">Archived</span>
		{:else}
			<Button variant="tonal" size="xs" href={'/orders/new?pickup=' + encodeURIComponent(location.id)}>Request</Button>
		{/if}
	</li>
{/snippet}

{#if authService.user}
	<section class="home" aria-labelledby="home-title">
		<h1 id="home-title" class="visually-hidden">Home</h1>
	{#if proximity.locating || proximity.error}<div class="location-status"><LocationStatus {proximity} /></div>{/if}

		<div class="map-panel">
			<CampusMap
				{markers}
				label={selectedOrder ? 'Map of the selected request' : mode === 'requests' ? 'Map of open requests' : 'Map of supplier locations'}
				selectedId={selectedOrder ? undefined : selection?.id}
				route={selectedOrder ? ['pickup', 'destination'] : undefined}
				onselect={selectedOrder ? undefined : selectMarker}
			/>
			<div class="map-toggle" role="group" aria-label="Show on map">
				<Button variant={mode === 'suppliers' ? 'filled' : 'tonal'} size="s" aria-pressed={mode === 'suppliers'} onclick={() => setMode('suppliers')}>Suppliers</Button>
				<Button variant={mode === 'requests' ? 'filled' : 'tonal'} size="s" aria-pressed={mode === 'requests'} disabled={suspended} onclick={() => setMode('requests')}>Open requests</Button>
			</div>
			{#if selectedOrder}
				<p class="map-legend"><span class="dot pickup"></span>Pick up <span class="dot destination"></span>Deliver to</p>
			{/if}
		</div>

		<Card variant="outlined">
			<div class="panel" aria-labelledby="open-title">
				<header class="panel-head">
					<h2 id="open-title">Open requests</h2>
					<div class="panel-actions">
						<SelectOutlined
							label="Request order"
							value={requestSort}
							options={[{ value: 'proximity', text: 'Nearest first' }, { value: 'newest', text: 'Newest first' }]}
							onchange={(e) => requestSort = e.currentTarget.value as typeof requestSort}
						/>
						<Button variant="text" size="s" href={'/orders?sort=' + (origin ? requestSort : 'newest')}>See all</Button>
					</div>
				</header>

				{#if suspended}
					<p class="notice">Your account is suspended, so you can't deliver requests.</p>
				{:else if openLoading}
					<div class="loading"><LoadingIndicator aria-label="Loading open requests" /></div>
				{:else if openError}
					<p class="notice error" role="alert">{openError}</p>
				{:else if !openOrders.length}
					<p class="empty">No open requests right now.</p>
				{:else}
					<ul class="rows">
						{#each sortedOrders as order (order.id)}
							{@const selected = selection?.kind === 'request' && selection.id === order.id}
							<li class="row" class:selected>
								<button type="button" class="row-main" aria-pressed={selected} onclick={() => selectRequest(order.id)}>
									<span class="title-line"><span class="title">{order.itemDescription}</span><span class="reward">{order.rewardCredits.toString()} credits</span></span>
									<span class="meta">From {order.pickup?.name ?? 'pickup'} to {order.deliveryBuildingName || 'an unlisted area'}{distanceTo(order) ? ', ' + distanceTo(order) + ' away' : ''}</span>
								</button>
								<Button variant="tonal" size="xs" href={'/orders/' + encodeURIComponent(order.id)}>View</Button>
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		</Card>

		<Card variant="outlined">
			<div class="panel" aria-labelledby="request-title">
				<header class="panel-head">
					<h2 id="request-title">Request delivery</h2>
					<div class="panel-actions">
						<SelectOutlined
							label="Supplier order"
							value={supplierSort}
							options={[{ value: 'proximity', text: 'Nearest first' }, { value: 'name', text: 'Name (A–Z)' }]}
							onchange={(e) => supplierSort = e.currentTarget.value as typeof supplierSort}
						/>
						<Button variant="text" size="s" href={'/suppliers?suppliers=1&sort=' + (origin ? supplierSort : 'name')}>All suppliers</Button>
					</div>
				</header>
				<form class="search" role="search" onsubmit={(event) => findSuppliers(event)}>
					<TextFieldOutlined
						label="Search suppliers"
						bind:value={search}
						trailing={{ icon: searching ? iconHourglass : iconSearch, 'aria-label': 'Search', disabled: searching, onclick: () => findSuppliers() }}
					/>
				</form>
				{#if supplierError}<p class="notice error" role="alert">{supplierError}</p>{/if}

				{#if searchResults}
					<div class="group-head">
						<h3>{searchResults.length ? 'Results' : 'No suppliers match'}</h3>
						<Button variant="text" size="xs" onclick={clearSearch}>Clear</Button>
					</div>
					<ul class="rows">{#each searchResults as location (location.id)}{@render supplierItem(location)}{/each}</ul>
				{:else if suppliersLoading}
					<div class="loading"><LoadingIndicator aria-label="Loading suppliers" /></div>
				{:else}
					{#if recentSuppliers.length}
						<h3>Recent</h3>
						<ul class="rows">{#each recentSuppliers as location (location.id)}{@render supplierItem(location)}{/each}</ul>
					{/if}
					<h3>Suppliers</h3>
					{#if !suppliers.length}<p class="empty">No suppliers yet.</p>{/if}
					<ul class="rows">{#each suppliers as location (location.id)}{@render supplierItem(location)}{/each}</ul>
				{/if}
			</div>
		</Card>
	</section>
{/if}

<style>
	/* Wireframe: map across the top, open requests and request delivery side by side. */
	.location-status { grid-column: 1 / -1; }
	.home {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 1rem;
		max-width: 76rem;
		margin: 0 auto;
	}

	.map-panel {
		grid-column: 1 / -1;
		position: relative;
		min-height: 16rem;
		height: 42vh;
	}

	.map-toggle {
		position: absolute;
		top: 0.75rem;
		right: 0.75rem;
		z-index: 500;
		display: flex;
		gap: 0.25rem;
		padding: 0.25rem;
		border-radius: var(--m3-shape-full, 999px);
		background: var(--m3c-surface-container-lowest, #fff);
		box-shadow: 0 1px 4px rgb(0 0 0 / 0.25);
	}

	.map-legend {
		position: absolute;
		left: 0.75rem;
		bottom: 0.75rem;
		z-index: 500;
		display: flex;
		align-items: center;
		gap: 0.4rem;
		margin: 0;
		padding: 0.35rem 0.75rem;
		border-radius: var(--m3-shape-full, 999px);
		background: var(--m3c-surface-container-lowest, #fff);
		font-size: 0.85rem;
		box-shadow: 0 1px 4px rgb(0 0 0 / 0.25);
	}

	.dot {
		width: 0.7rem;
		height: 0.7rem;
		border-radius: 50%;
		display: inline-block;
	}

	.dot.pickup { background: var(--m3c-tertiary); }
	.dot.destination { background: var(--m3c-error); margin-inline-start: 0.5rem; }

	.panel {
		display: grid;
		align-content: start;
		gap: 0.75rem;
		padding: 0.25rem;
		min-width: 0;
	}

	.panel-head, .group-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.25rem 0.5rem;
	}

	.panel-head h2 { white-space: nowrap; }

	.panel-actions { display: flex; gap: 0.25rem; flex-wrap: wrap; align-items: center; }

	h2 { margin: 0; font-size: 1.15rem; }

	h3 {
		margin: 0.25rem 0 0;
		font-size: 0.875rem;
		font-weight: 600;
		color: var(--m3c-on-surface-variant);
	}

	.closed {
		font-size: 0.8rem;
		font-weight: 600;
		color: var(--m3c-error);
		white-space: nowrap;
	}

	/* A compact, full-width field: the panel's lists, not the input, carry the weight. */
	.search { display: grid; --foc-field-shrink: 0.75rem; }

	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0.25rem;
		max-height: 24rem;
		overflow-y: auto;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding-inline-end: 0.5rem;
		border-radius: var(--m3-shape-medium);
	}

	.row:hover { background: var(--m3c-surface-container); }

	.row.selected {
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
	}

	.row-main {
		flex: 1;
		min-width: 0;
		display: grid;
		gap: 0.1rem;
		padding: 0.6rem 0.75rem;
		border: 0;
		background: none;
		color: inherit;
		text-align: start;
		cursor: pointer;
		border-radius: inherit;
	}

	.title {
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.meta {
		font-size: 0.85rem;
		color: var(--m3c-on-surface-variant);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.title-line {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
		min-width: 0;
	}

	.reward { flex: 0 0 auto; font-size: 0.9rem; font-weight: 650; white-space: nowrap; }

	.empty { margin: 0; color: var(--m3c-on-surface-variant); }

	.loading { display: flex; justify-content: center; padding: 1rem; }

	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	@media (max-width: 60rem) {
		.map-panel { height: 18rem; }
		.home {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
