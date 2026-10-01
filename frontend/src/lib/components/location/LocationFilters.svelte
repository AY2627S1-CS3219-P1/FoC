<script lang="ts">
	import { Checkbox, SelectOutlined, TextFieldOutlined } from 'm3-svelte';
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

	const buildingOptions = $derived([
		{ value: '', text: 'All buildings' },
		...buildings.map((b) => ({ value: b.id, text: b.name }))
	]);
	const categoryOptions = $derived([
		{ value: '', text: 'All categories' },
		...categories.map((c) => ({ value: c.id, text: c.name }))
	]);
	const sortOptions = [
		{ value: 'name-asc', text: 'Name (A–Z)' },
		{ value: 'name-desc', text: 'Name (Z–A)' },
		{ value: 'building-asc', text: 'Building (A–Z)' },
		{ value: 'building-desc', text: 'Building (Z–A)' }
	];
	const archiveOptions = [
		{ value: 'active', text: 'Active' },
		{ value: 'archived', text: 'Archived' },
		{ value: 'all', text: 'All' }
	];
</script>

<form class="filters" role="search" onsubmit={(e) => e.preventDefault()}>
	<div class="search">
		<TextFieldOutlined
			label="Search by name"
			type="search"
			maxlength={200}
			bind:value={searchText}
			oninput={onSearchInput}
		/>
	</div>

	<SelectOutlined
		label="Building"
		options={buildingOptions}
		value={query.buildingId ?? ''}
		onchange={(e) => onchange({ buildingId: e.currentTarget.value || undefined })}
	/>

	<SelectOutlined
		label="Category"
		options={categoryOptions}
		value={query.categoryId ?? ''}
		onchange={(e) => onchange({ categoryId: e.currentTarget.value || undefined })}
	/>

	<SelectOutlined
		label="Sort by"
		options={sortOptions}
		value={`${query.sort ?? 'name'}-${query.direction ?? 'asc'}`}
		onchange={(e) => onSortChange(e.currentTarget.value)}
	/>

	{#if isAdmin}
		<SelectOutlined
			label="Status"
			options={archiveOptions}
			value={query.archive ?? 'active'}
			onchange={(e) => onchange({ archive: e.currentTarget.value as ArchiveView })}
		/>
	{/if}

	<label class="checkbox">
		<Checkbox>
			<input
				type="checkbox"
				checked={query.suppliersOnly}
				onchange={(e) => onchange({ suppliersOnly: e.currentTarget.checked || undefined })}
			/>
		</Checkbox>
		<span>Suppliers only</span>
	</label>
</form>

<style>
	.filters {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
		gap: 0.75rem 1rem;
		align-items: center;
	}

	.filters > :global(.m3-container),
	.search > :global(.m3-container) {
		width: 100%;
		min-width: 0;
	}

	.search {
		grid-column: 1 / -1;
	}

	.checkbox {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		cursor: pointer;
	}

	@media (max-width: 36rem) {
		.filters {
			grid-template-columns: 1fr;
		}
	}
</style>
