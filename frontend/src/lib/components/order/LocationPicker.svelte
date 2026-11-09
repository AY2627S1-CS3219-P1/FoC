<script lang="ts">
	import { Button, TextFieldOutlined } from 'm3-svelte';
	import iconSearch from '@ktibow/iconset-material-symbols/search';
	import { locationApi } from '$lib/services';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';

	let { label, value = $bindable(''), disabled = false, onchange, onresolve, onclear }: {
		label: string; value?: string; disabled?: boolean;
		/** The user chose a location. */
		onchange?: (location: Location) => void;
		/** The selected location is known, whether chosen here or loaded for an existing value. */
		onresolve?: (location: Location) => void;
		/** The user removed the selection. */
		onclear?: () => void;
	} = $props();
	let search = $state('');
	let results = $state<Location[]>([]);
	let selected = $state<Location | null>(null);
	let loading = $state(false);
	let error = $state('');
	// The id whose details couldn't be loaded, so the picker can offer to replace it.
	let unresolved = $state('');
	let generation = 0;

	function clear() {
		value = '';
		selected = null;
		unresolved = '';
		onclear?.();
	}

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
		onresolve?.(location);
	}

	// A value set from outside (a link's ?pickup=, a restored draft) still needs its name.
	$effect(() => {
		const id = value;
		unresolved = '';
		if (!id || selected?.id === id) return;
		let stale = false;
		locationApi.getLocation(id).then(
			(location) => {
				if (stale || value !== id) return;
				selected = location;
				onresolve?.(location);
			},
			() => {
				if (!stale && value === id) unresolved = id;
			}
		);
		return () => (stale = true);
	});
</script>

<fieldset {disabled} class="picker">
	<legend>{label}</legend>
	{#if value && selected?.id === value}
		<div class="selected" role="status">
			<span class="selected-text">
				<span class="selected-name">{selected.name}</span>
				<span class="selected-meta">{selected.building?.name ?? ''}{selected.floor ? ', ' + selected.floor : ''}</span>
			</span>
			<Button type="button" variant="text" size="s" {disabled} onclick={clear}>Change</Button>
		</div>
		{#if selected.currentDisablement}<p class="notice">This location is not operating: {selected.currentDisablement.reason}. You can still select it.</p>{/if}
	{:else}
		<!-- Not a <form>: the picker sits inside other forms. Enter searches instead of submitting them. -->
		<div class="search" role="search" onkeydowncapture={(event) => { if (event.key === 'Enter') { event.preventDefault(); void find(); } }}>
			<TextFieldOutlined
				label="Search locations"
				aria-label={label + ' search'}
				bind:value={search}
				{disabled}
				trailing={{ icon: iconSearch, 'aria-label': 'Search ' + label.toLowerCase(), disabled: loading || disabled, onclick: () => void find() }}
			/>
		</div>
		{#if value && unresolved === value}
			<div class="selected unresolved" role="alert">
				<span class="selected-meta">The chosen location couldn't be loaded. It may have been removed.</span>
				<Button type="button" variant="text" size="s" {disabled} onclick={clear}>Clear</Button>
			</div>
		{:else if value}<p class="selected-meta" role="status">Loading the selected location…</p>{/if}
		{#if loading}<p class="selected-meta" role="status">Searching…</p>{/if}
	{/if}
	{#if error}<p class="notice error" role="alert">{error}</p>{/if}
	{#if results.length}
		<ul>
			{#each results as location (location.id)}
				<li>
					<button type="button" class="result" onclick={() => choose(location)}>
						<span class="selected-name">{location.name}</span>
						<span class="selected-meta">{location.building?.name ?? 'Building unavailable'}{location.floor ? ', ' + location.floor : ''}</span>
					</button>
				</li>
			{/each}
		</ul>
		{#if results.length === 20}<small class="selected-meta">Showing the first 20 matches. Refine your search if needed.</small>{/if}
	{/if}
</fieldset>

<style>
	.picker { display: grid; gap: .5rem; border: 0; padding: 0; margin: 0; min-width: 0; }
	legend { padding: 0; margin-bottom: .5rem; font-size: .875rem; font-weight: 600; color: var(--m3c-on-surface-variant); }
	/* A grid cell stretches the field to full width; the variable makes it compact. */
	.search { display: grid; --foc-field-shrink: 0.75rem; }
	.selected { display: flex; align-items: center; justify-content: space-between; gap: .5rem; padding: .5rem .5rem .5rem 1rem; border-radius: var(--m3-shape-medium); background: var(--m3c-secondary-container); color: var(--m3c-on-secondary-container); }
	.selected.unresolved { background: var(--m3c-error-container); color: var(--m3c-on-error-container); }
	.selected-text { display: grid; min-width: 0; overflow-wrap: anywhere; }
	.selected-name { font-weight: 600; }
	.selected-meta { margin: 0; font-size: .85rem; color: var(--m3c-on-surface-variant); }
	ul { list-style: none; padding: 0; margin: 0; display: grid; gap: .15rem; max-height: 16rem; overflow-y: auto; }
	.result { display: grid; width: 100%; padding: .5rem 1rem; border: 0; border-radius: var(--m3-shape-medium); background: none; color: inherit; font: inherit; text-align: start; cursor: pointer; }
	.result:hover { background: var(--m3c-surface-container); }
	.result:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: -2px; }
</style>
