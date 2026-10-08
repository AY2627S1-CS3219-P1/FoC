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

	// Wait for a pause in typing before searching.
	// Cancelled on unmount so leaving the page cannot navigate back here.
	let searchText = $state('');
	let searchTimer: ReturnType<typeof setTimeout>;
	$effect(() => () => clearTimeout(searchTimer));
	function onSearchInput() {
		const typed = searchText;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			if (typed !== urlSearch) submitted = typed;
			onchange({ search: typed || undefined });
		}, 300);
	}

	// Follows the URL (back button, "Clear filters") but not every keystroke.
	// An outside change cancels the pending search so it cannot restore old text.
	// Our own search arriving in the URL leaves newer typing alone.
	const urlSearch = $derived(query.search ?? '');
	let submitted: string | undefined;
	$effect(() => {
		const next = urlSearch;
		if (next === submitted) {
			submitted = undefined;
			return;
		}
		clearTimeout(searchTimer);
		searchText = next;
	});

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

	/*
	 * m3-svelte sizes the menu to fit every option; long lists scroll instead.
	 * Its menu grows from zero height, which leaves a scrollable menu scrolled
	 * past the first option, so this menu fades in at full height.
	 */
	.filters :global(select:open::picker(select)) {
		max-height: min(20rem, 50vh);
		overflow-y: auto;
		transition:
			opacity var(--m3-easing-fast),
			display var(--m3-duration-fast) allow-discrete,
			overlay var(--m3-duration-fast) allow-discrete;
	}
	@starting-style {
		.filters :global(select:open::picker(select)) {
			height: auto;
			opacity: 0;
		}
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
