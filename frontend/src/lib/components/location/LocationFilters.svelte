<script lang="ts">
	import type { Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import type {
		ArchiveView,
		LocationQuery,
		SortDirection,
		SortField
	} from '$lib/services/location';

	let {
		query,
		buildings,
		categories,
		isAdmin,
		onchange
	}: {
		query: LocationQuery;
		buildings: Building[];
		categories: Category[];
		isAdmin: boolean;
		onchange: (changes: Partial<LocationQuery>) => void;
	} = $props();

	// Follows the URL (back button, "Clear filters") but not every keystroke.
	let searchText = $state('');
	$effect(() => {
		searchText = query.search ?? '';
	});

	// Wait for a pause in typing before searching.
	// Cancelled on unmount so leaving the page cannot navigate back here.
	let searchTimer: ReturnType<typeof setTimeout>;
	$effect(() => () => clearTimeout(searchTimer));
	function onSearchInput() {
		const typed = searchText;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => onchange({ search: typed || undefined }), 300);
	}

	function onSortChange(value: string) {
		const [sort, direction] = value.split('-') as [SortField, SortDirection];
		onchange({ sort, direction });
	}
</script>

<form class="filters" role="search" onsubmit={(e) => e.preventDefault()}>
	<label class="search">
		<span>Search by name</span>
		<input
			type="search"
			placeholder="e.g. co-op, supper"
			maxlength="200"
			bind:value={searchText}
			oninput={onSearchInput}
		/>
	</label>

	<label>
		<span>Building</span>
		<select
			value={query.buildingId ?? ''}
			onchange={(e) => onchange({ buildingId: e.currentTarget.value || undefined })}
		>
			<option value="">All buildings</option>
			{#each buildings as building (building.id)}
				<option value={building.id}>{building.name}</option>
			{/each}
		</select>
	</label>

	<label>
		<span>Category</span>
		<select
			value={query.categoryId ?? ''}
			onchange={(e) => onchange({ categoryId: e.currentTarget.value || undefined })}
		>
			<option value="">All categories</option>
			{#each categories as category (category.id)}
				<option value={category.id}>{category.name}</option>
			{/each}
		</select>
	</label>

	<label>
		<span>Sort by</span>
		<select
			value={`${query.sort ?? 'name'}-${query.direction ?? 'asc'}`}
			onchange={(e) => onSortChange(e.currentTarget.value)}
		>
			<option value="name-asc">Name (A–Z)</option>
			<option value="name-desc">Name (Z–A)</option>
			<option value="building-asc">Building (A–Z)</option>
			<option value="building-desc">Building (Z–A)</option>
		</select>
	</label>

	{#if isAdmin}
		<label>
			<span>Status</span>
			<select
				value={query.archive ?? 'active'}
				onchange={(e) => onchange({ archive: e.currentTarget.value as ArchiveView })}
			>
				<option value="active">Active</option>
				<option value="archived">Archived</option>
				<option value="all">All</option>
			</select>
		</label>
	{/if}

	<label class="checkbox">
		<input
			type="checkbox"
			checked={query.suppliersOnly}
			onchange={(e) => onchange({ suppliersOnly: e.currentTarget.checked || undefined })}
		/>
		<span>Suppliers only</span>
	</label>
</form>

<style>
	.filters {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
		gap: 0.75rem 1rem;
		align-items: end;
		margin-bottom: 1.5rem;
	}

	label {
		display: grid;
		gap: 0.3rem;
		font-size: 0.875rem;
		font-weight: 600;
	}

	.search {
		grid-column: 1 / -1;
	}

	.checkbox {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.5rem;
	}

	input[type='search'],
	select {
		width: 100%;
		min-height: 2.5rem;
		padding: 0.4rem 0.6rem;
		border: 1px solid #b9c6d9;
		border-radius: 0.4rem;
		background: #fff;
		font: inherit;
	}

	@media (max-width: 40rem) {
		.filters {
			grid-template-columns: 1fr 1fr;
		}
	}
</style>
