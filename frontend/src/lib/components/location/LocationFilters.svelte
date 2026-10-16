<script lang="ts">
	import { Button, Checkbox, SelectOutlined, TextFieldOutlined } from 'm3-svelte';
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

	const advancedOptionsId = $props.id();
	let advancedOpen = $state(false);

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
		{ value: 'active', text: 'Unarchived' },
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
		<Button
			type="button"
			variant="text"
			size="xs"
			iconType="full"
			aria-label={advancedOpen ? 'Hide advanced search' : 'Show advanced search'}
			aria-expanded={advancedOpen}
			aria-controls={advancedOptionsId}
			title="Advanced search"
			onclick={() => (advancedOpen = !advancedOpen)}
		>
			<svg
				class:expanded={advancedOpen}
				width="20"
				height="20"
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="2"
				stroke-linecap="round"
				stroke-linejoin="round"
				aria-hidden="true"
			>
				<path d="m6 9 6 6 6-6" />
			</svg>
		</Button>
	</div>

	<div id={advancedOptionsId} class="advanced-options" hidden={!advancedOpen}>
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
	</div>
</form>

<style>
	.filters {
		display: grid;
		gap: 0.75rem;
	}

	.search {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.5rem;
	}

	svg {
		transition: transform 150ms ease;
	}

	.expanded {
		transform: rotate(180deg);
	}

	.advanced-options {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
		gap: 0.75rem 1rem;
		align-items: center;
	}

	.advanced-options[hidden] {
		display: none;
	}

	.advanced-options > :global(.m3-container),
	.search > :global(.m3-container:first-child) {
		width: 100%;
		min-width: 0;
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

	@media (prefers-reduced-motion: reduce) {
		svg {
			transition: none;
		}
	}

	@media (max-width: 36rem) {
		.advanced-options {
			grid-template-columns: 1fr;
		}
	}
</style>
