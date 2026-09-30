<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
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

<section>
	<h1>Campus locations</h1>

	<LocationFilters {query} {buildings} {categories} {isAdmin} onchange={update} />

	<div aria-live="polite">
		{#if error}
			<Notice tone="error">
				{error}
				{#if !authService.isAuthenticated}<a href="/login">Log in</a>{/if}
			</Notice>
		{:else if loading && !result}
			<Notice>Loading locations…</Notice>
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
					<button
						type="button"
						disabled={result.page <= 1}
						onclick={() => update({ page: result!.page - 1 })}>Previous</button
					>
					<span>Page {result.page} of {result.totalPages}</span>
					<button
						type="button"
						disabled={result.page >= result.totalPages}
						onclick={() => update({ page: result!.page + 1 })}>Next</button
					>
				</nav>
			{/if}
		{/if}
	</div>
</section>

<style>
	h1 {
		font-size: clamp(1.75rem, 4vw, 2.5rem);
	}

	.summary {
		font-weight: 600;
	}

	.muted {
		color: #527099;
		font-weight: 400;
	}

	.cards {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(16rem, 1fr));
		gap: 1rem;
		padding: 0;
		list-style: none;
	}

	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 1rem;
		margin-top: 1.5rem;
	}

	.pagination button {
		min-height: 2.5rem;
		padding: 0 1rem;
		border: 1px solid #b9c6d9;
		border-radius: 0.4rem;
		background: #fff;
		font: inherit;
		cursor: pointer;
	}

	.pagination button:disabled {
		opacity: 0.5;
		cursor: default;
	}
</style>
