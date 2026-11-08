<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import LocationDetails from '$lib/components/location/LocationDetails.svelte';
	import LocationFilters from '$lib/components/location/LocationFilters.svelte';
	import Notice from '$lib/components/location/Notice.svelte';
	import CampusMap, { type MapMarker } from '$lib/components/map/CampusMap.svelte';
	import type { Building, Category, Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, locationApi } from '$lib/services';
	import type { LocationPage, LocationQuery } from '$lib/services/location';
	import { describeLocationError } from '$lib/services/location/errors';
	import { formatOpeningHours } from '$lib/services/location/format';
	import { paramsFromQuery, queryFromParams } from '$lib/services/location/query';

	const query = $derived(queryFromParams(page.url.searchParams));
	const selectedId = $derived(page.url.searchParams.get('selected') ?? '');
	const isAdmin = $derived(
		authService.user?.role === UserRole.ADMIN || authService.user?.role === UserRole.SUPER_ADMIN
	);

	let result = $state<LocationPage | null>(null);
	let error = $state<string | null>(null);
	let loading = $state(true);
	let buildings = $state<Building[]>([]);
	let categories = $state<Category[]>([]);

	let referenceError = $state('');
	let referenceReload = $state(0),
		reload = $state(0);
	$effect(() => {
		void referenceReload;
		let stale = false;
		referenceError = '';
		Promise.all([locationApi.listBuildings(), locationApi.listCategories()])
			.then(([b, c]) => {
				if (!stale) {
					buildings = b;
					categories = c;
				}
			})
			.catch(() => {
				if (!stale) referenceError = 'Building and category filters could not be loaded.';
			});
		return () => {
			stale = true;
		};
	});

	// Refetch whenever the URL query changes; ignore responses that arrive late.
	$effect(() => {
		const current = query;
		void reload;
		let stale = false;
		loading = true;
		error = null;
		locationApi
			.listLocations({ ...current, search: current.search?.trim() })
			.then((locations) => !stale && (result = locations))
			.catch((e) => !stale && (error = describeLocationError(e)))
			.finally(() => !stale && (loading = false));
		return () => (stale = true);
	});

	// The selected Location may be on another page of results, so load it by id.
	let selected = $state<Location | null>(null);
	let selectedError = $state('');
	$effect(() => {
		const id = selectedId;
		const listed = result?.locations.find((location) => location.id === id);
		selectedError = '';
		if (!id) {
			selected = null;
			return;
		}
		if (listed) {
			selected = listed;
			return;
		}
		let stale = false;
		selected = null;
		locationApi
			.getLocation(id)
			.then((found) => !stale && (selected = found))
			.catch((e) => !stale && (selectedError = describeLocationError(e)));
		return () => (stale = true);
	});

	/** Updates the URL with changes, returning to page 1 unless the page changed. */
	function update(changes: Partial<LocationQuery>) {
		const search = paramsFromQuery({ ...query, page: undefined, ...changes }).toString();
		const paginating = changes.page !== undefined;
		goto(search ? `?${search}` : page.url.pathname, { keepFocus: !paginating, noScroll: !paginating });
	}

	function selectionHref(id: string | null): string {
		const params = paramsFromQuery(query);
		if (id) params.set('selected', id);
		const search = params.toString();
		return search ? `?${search}` : page.url.pathname;
	}

	const markers = $derived<MapMarker[]>(
		(result?.locations ?? []).flatMap((location) =>
			location.coordinates
				? [
						{
							id: location.id,
							latitude: location.coordinates.latitude,
							longitude: location.coordinates.longitude,
							label: location.name,
							kind: 'supplier' as const
						}
					]
				: []
		)
	);
</script>

<svelte:head><title>Suppliers · Friend on Campus</title></svelte:head>

<section class="split" class:has-selection={Boolean(selectedId)} aria-labelledby="locations-title">
	<div class="list-pane">
		<h1 id="locations-title">Suppliers</h1>

		{#if referenceError}
			<p class="notice error" role="alert">{referenceError}</p>
			<Button variant="text" onclick={() => referenceReload++}>Retry filters</Button>
		{/if}
		<LocationFilters {query} {buildings} {categories} {isAdmin} onchange={update} />

		<div class="results" aria-live="polite">
			{#if error}
				<Notice tone="error">
					{error}
					<Button variant="text" onclick={() => reload++}>Retry locations</Button>
					{#if !authService.isAuthenticated}<a href="/login">Log in</a>{/if}
				</Notice>
			{:else if loading && !result}
				<div class="loading-state">
					<LoadingIndicator aria-label="Loading locations" />
				</div>
			{:else if result && result.locations.length === 0}
				<Notice>
					No locations match these filters.
					<a href={page.url.pathname}>Clear filters</a>
				</Notice>
			{:else if result}
				<p class="summary">
					{result.totalItems}
					{result.totalItems === 1 ? 'location' : 'locations'}{#if loading}<span class="muted">, updating</span>{/if}
				</p>

				<ul class="rows">
					{#each result.locations as location (location.id)}
						<li>
							<a
								class="row"
								class:archived={location.archivedAt}
								href={selectionHref(location.id)}
								aria-current={location.id === selectedId ? 'true' : undefined}
								data-sveltekit-noscroll
								data-sveltekit-keepfocus
							>
								<span class="title">{location.name}</span>
								<span class="meta">
									{location.building?.name ?? ''}{location.floor ? ', ' + location.floor : ''}
								</span>
								<span class="meta">{formatOpeningHours(location.opensAt, location.closesAt)}</span>
								{#if location.currentDisablement}<span class="closed">Temporarily closed</span>{/if}
							</a>
						</li>
					{/each}
				</ul>

				{#if result.totalPages > 1}
					<nav class="pagination" aria-label="Pages">
						<Button
							type="button"
							variant="outlined"
							disabled={loading || result.page <= 1}
							onclick={() => update({ page: result!.page - 1 })}>Previous</Button
						>
						<span>Page {result.page} of {result.totalPages}</span>
						<Button
							type="button"
							variant="outlined"
							disabled={loading || result.page >= result.totalPages}
							onclick={() => update({ page: result!.page + 1 })}>Next</Button
						>
					</nav>
				{/if}
			{/if}
		</div>
	</div>

	<div class="detail-pane">
		<Card variant="outlined">
			<div class="panel-body">
				{#if selectedId}
					<div class="back">
						<Button variant="text" href={selectionHref(null)} data-sveltekit-noscroll>Back to list</Button>
					</div>
					{#if selectedError}
						<Notice tone="error">{selectedError}</Notice>
					{:else if !selected}
						<div class="loading-state"><LoadingIndicator aria-label="Loading supplier" /></div>
					{:else}
						<LocationDetails location={selected} headingLevel={2}>
							{#snippet actions()}
								{#if selected && !selected.archivedAt}
									<Button href={'/orders/new?pickup=' + encodeURIComponent(selected.id)}>Make a request</Button>
								{/if}
								{#if isAdmin && selected}
									<Button variant="outlined" href={`/admin/suppliers/${selected.id}`}>Edit</Button>
								{/if}
							{/snippet}
						</LocationDetails>
					{/if}
				{:else}
					<div class="overview-map">
						<CampusMap
							{markers}
							label="Map of the listed locations"
							onselect={(id) => goto(selectionHref(id), { noScroll: true, keepFocus: true })}
						/>
					</div>
					<p class="muted">Choose a supplier to see its hours and make a request.</p>
				{/if}
			</div>
		</Card>
	</div>
</section>

<style>
	/* Wireframe: list on the left, the selected supplier on the right. */
	.split {
		display: grid;
		grid-template-columns: minmax(18rem, 2fr) minmax(0, 3fr);
		gap: 1.5rem;
		align-items: start;
		max-width: 76rem;
		margin: 0 auto;
	}

	.list-pane {
		display: grid;
		gap: 1rem;
		min-width: 0;
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

	.overview-map {
		height: min(60vh, 32rem);
	}

	.back {
		display: none;
	}

	.results {
		display: grid;
		gap: 0.75rem;
	}

	.summary {
		margin: 0;
		font-weight: 600;
	}

	.muted {
		margin: 0;
		color: var(--m3c-on-surface-variant);
		font-weight: 400;
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

	.row.archived .title {
		color: var(--m3c-on-surface-variant);
	}

	.title {
		font-weight: 600;
	}

	.meta {
		font-size: 0.875rem;
		color: var(--m3c-on-surface-variant);
	}

	.closed {
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--m3c-error);
	}

	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 1rem;
	}

	/* One pane at a time on narrow screens: the list, or the selected supplier. */
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
