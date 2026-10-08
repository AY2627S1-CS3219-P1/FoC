<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import LocationCard from '$lib/components/location/LocationCard.svelte';
	import LocationFilters from '$lib/components/location/LocationFilters.svelte';
	import Notice from '$lib/components/location/Notice.svelte';
	import type { Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, locationApi } from '$lib/services';
	import type { LocationPage, LocationQuery } from '$lib/services/location';
	import { describeLocationError } from '$lib/services/location/errors';
	import { paramsFromQuery, queryFromParams } from '$lib/services/location/query';

	const query = $derived(queryFromParams(page.url.searchParams));
	const isAdmin = $derived(
		authService.user?.role === UserRole.ADMIN || authService.user?.role === UserRole.SUPER_ADMIN
	);

	let result = $state<LocationPage | null>(null);
	let error = $state<string | null>(null);
	let loading = $state(true);
	let buildings = $state<Building[]>([]);
	let categories = $state<Category[]>([]);

	// Reference data for the filter menus. Failures only leave the menus empty.
	$effect(() => {
		locationApi.listBuildings().then((b) => (buildings = b), () => {});
		locationApi.listCategories().then((c) => (categories = c), () => {});
	});

	// Refetch whenever the URL query changes; ignore responses that arrive late.
	$effect(() => {
		const current = query;
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

	/** Updates the URL with changes, returning to page 1 unless the page changed. */
	function update(changes: Partial<LocationQuery>) {
		const search = paramsFromQuery({ ...query, page: undefined, ...changes }).toString();
		goto(search ? `?${search}` : page.url.pathname, { keepFocus: true, noScroll: true });
	}
</script>

<svelte:head><title>Campus locations · Friend on Campus</title></svelte:head>

<section class="page-content locations-page" aria-labelledby="locations-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Locations</p>
			<h1 id="locations-title">Campus locations</h1>
			<p>Browse stores and facilities on campus that can be picked up from.</p>
		</div>
	</header>

	<Card variant="outlined">
		<div class="panel-body">
			<LocationFilters {query} {buildings} {categories} {isAdmin} onchange={update} />
		</div>
	</Card>

	<div class="results" aria-live="polite">
		{#if error}
			<Notice tone="error">
				{error}
				{#if !authService.isAuthenticated}<a href="/login">Log in</a>{/if}
			</Notice>
		{:else if loading && !result}
			<div class="loading-state">
				<LoadingIndicator aria-label="Loading locations" />
				<p>Loading locations…</p>
			</div>
		{:else if result && result.locations.length === 0}
			<Notice>
				No locations match these filters.
				<a href={page.url.pathname}>Clear filters</a>
			</Notice>
		{:else if result}
			<p class="summary">
				{result.totalItems}
				{result.totalItems === 1 ? 'location' : 'locations'}
				{#if loading}<span class="muted">· updating…</span>{/if}
			</p>

			<ul class="cards">
				{#each result.locations as location (location.id)}
					<li><LocationCard {location} /></li>
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
</section>

<style>
	.locations-page {
		max-width: none;
	}

	.results {
		display: grid;
		gap: 1rem;
	}

	.summary {
		margin: 0;
		font-weight: 600;
	}

	.muted {
		color: var(--m3c-on-surface-variant);
		font-weight: 400;
	}

	.cards {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(16rem, 1fr));
		gap: 1rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 1rem;
	}
</style>
