<script lang="ts">
	import { Button, TextFieldOutlined } from 'm3-svelte';
	import { locationApi } from '$lib/services';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';

	let { label, value = $bindable(''), disabled = false, onchange }: {
		label: string; value?: string; disabled?: boolean; onchange?: (location: Location) => void;
	} = $props();
	let search = $state('');
	let results = $state<Location[]>([]);
	let selected = $state<Location | null>(null);
	let loading = $state(false);
	let error = $state('');
	let generation = 0;

	async function find() {
		const request = ++generation;
		loading = true;
		error = '';
		try {
			const page = await locationApi.listLocations({ search: search.trim(), pageSize: 20 });
			if (request === generation) results = page.locations;
		} catch {
			if (request === generation) error = 'Could not search locations. Keep your selection and try again.';
		} finally {
			if (request === generation) loading = false;
		}
	}

	function choose(location: Location) {
		selected = location;
		value = location.id;
		results = [];
		onchange?.(location);
	}
</script>

<fieldset {disabled} class="picker">
	<legend>{label}</legend>
	<div class="search">
		<TextFieldOutlined label="Search locations" aria-label={label + ' search'} bind:value={search} />
		<Button type="button" variant="outlined" disabled={loading || disabled} onclick={() => void find()}>{loading ? 'Searching…' : 'Search'}</Button>
	</div>
	{#if value}<p class="selected" role="status">Selected: {selected?.id === value ? selected.name + ' · ' + (selected.building?.name ?? '') : value}</p>{/if}
	{#if selected?.id === value && selected.currentDisablement}<p class="notice">This location is not operating: {selected.currentDisablement.reason}. You can still select it.</p>{/if}
	{#if error}<p class="notice error" role="alert">{error}</p>{/if}
	<ul>
		{#each results as location (location.id)}
			<li><Button type="button" variant="text" onclick={() => choose(location)}>{location.name} · {location.building?.name ?? 'Building unavailable'}{location.floor ? ' · ' + location.floor : ''}</Button></li>
		{/each}
	</ul>
	{#if results.length === 20}<small>Showing the first 20 matches. Refine your search if needed.</small>{/if}
</fieldset>

<style>
	.picker { display: grid; gap: .5rem; border: 1px solid var(--m3c-outline-variant); border-radius: 12px; padding: 1rem; min-width: 0; }
	.search { display: grid; grid-template-columns: minmax(0, 1fr); gap: .75rem; justify-items: start; }
	.search :global(.m3-container) { min-width: 0; max-width: 100%; width: 100%; }
	ul { list-style: none; padding: 0; margin: 0; }
	.selected { margin: 0; overflow-wrap: anywhere; }
</style>
