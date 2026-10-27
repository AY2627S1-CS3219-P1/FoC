<script lang="ts">
	import { Button, LoadingIndicator } from 'm3-svelte';
	import { locationApi } from '$lib/services';
	import type { Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import LocationEditor from '$lib/components/admin/LocationEditor.svelte';
	let buildings = $state<Building[]>([]),
		categories = $state<Category[]>([]);
	let loading = $state(true),
		error = $state(''),
		reload = $state(0);
	$effect(() => {
		void reload;
		let stale = false;
		loading = true;
		error = '';
		Promise.all([locationApi.listBuildings(), locationApi.listCategories()])
			.then(([b, c]) => {
				if (!stale) {
					buildings = b;
					categories = c;
				}
			})
			.catch(() => {
				if (!stale)
					error = 'Could not load buildings and categories. Retry before creating a location.';
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});
</script>

<svelte:head><title>Create location · Friend on Campus</title></svelte:head>
<section class="page-content admin-content">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1>Create location</h1>
			<p>Add a supplier or campus facility.</p>
		</div>
	</header>
	{#if error}<p class="notice error" role="alert">{error}</p>
		<Button onclick={() => reload++}>Retry reference data</Button>
	{:else if loading}<div class="loading-state" role="status">
			<LoadingIndicator aria-label="Loading reference data" />
			<p>Loading buildings and categories…</p>
		</div>
	{:else if buildings.length === 0}<p class="notice" role="status">
			No buildings are available. A building must be seeded before a location can be created.
		</p>
	{:else}<LocationEditor {buildings} {categories} />{/if}
</section>
