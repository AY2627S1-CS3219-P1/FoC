<script lang="ts">
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import type { Location, Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { locationApi, locationAdminApi } from '$lib/services';
	import { adminError } from '$lib/services/location-admin/errors';
	import { formatTimestamp } from '$lib/services/location-admin/format';
	import LocationEditor from '$lib/components/admin/LocationEditor.svelte';
	import ConfirmDialog from '$lib/components/admin/ConfirmDialog.svelte';
	import ClosureManager from '$lib/components/admin/ClosureManager.svelte';
	let location = $state<Location | null>(null);
	let buildings = $state<Building[]>([]),
		categories = $state<Category[]>([]);
	let referencesLoading = $state(true),
		referencesError = $state(''),
		referencesReload = $state(0);
	let loading = $state(true),
		error = $state(''),
		mutationError = $state(''),
		notice = $state('');
	let reload = $state(0),
		confirmOpen = $state(false),
		busy = $state(false);
	$effect(() => {
		const id = page.params.id ?? '';
		void reload;
		let stale = false;
		loading = true;
		error = '';
		mutationError = '';
		notice = '';
		confirmOpen = false;
		location = null;
		locationApi.getLocation(id)
			.then((value) => {
				if (!stale) location = value;
			})
			.catch((cause) => {
				if (!stale) error = adminError(cause);
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});
	$effect(() => {
		void referencesReload;
		let stale = false;
		referencesLoading = true;
		referencesError = '';
		Promise.all([locationApi.listBuildings(), locationApi.listCategories()])
			.then(([b, c]) => {
				if (!stale) {
					buildings = b;
					categories = c;
				}
			})
			.catch((cause) => {
				if (!stale) referencesError = adminError(cause);
			})
			.finally(() => {
				if (!stale) referencesLoading = false;
			});
		return () => {
			stale = true;
		};
	});

	async function transition() {
		if (!location || busy) return;
		const id = location.id;
		busy = true;
		mutationError = '';
		notice = '';
		try {
			const saved = location.archivedAt
				? await locationAdminApi.restore(location.id)
				: await locationAdminApi.archive(location.id);
			if (page.params.id !== id) return;
			location = saved;
			confirmOpen = false;
			notice = location.archivedAt ? 'Location archived.' : 'Location restored.';
		} catch (cause) {
			if (page.params.id !== id) return;
			confirmOpen = false;
			mutationError = adminError(cause);
		} finally {
			busy = false;
		}
	}
	async function refreshLocation(id: string) {
		if (page.params.id !== id) return;
		try {
			const updated = await locationApi.getLocation(id);
			if (page.params.id === id) location = updated;
		} catch (cause) {
			if (page.params.id === id) mutationError = adminError(cause);
		}
	}
</script>

<svelte:head><title>{location?.name ?? 'Manage location'} · Friend on Campus</title></svelte:head>
<section class="page-content admin-content">
	<div><Button variant="text" href="/admin/suppliers">← All locations</Button></div>
	{#if error}<p class="notice error" role="alert">{error}</p>
		<Button onclick={() => reload++}>Retry location</Button>
	{:else if loading}<div class="loading-state" role="status">
			<LoadingIndicator aria-label="Loading location" />
			<p>Loading location…</p>
		</div>
	{:else if location}
		<header class="page-heading">
			<div>
				<p class="eyebrow">{location.isSupplier ? 'Supplier' : 'Facility'}</p>
				<h1>{location.name}</h1>
				<p>
					{location.building?.name} · Updated {formatTimestamp(location.updatedAt)}
				</p>
			</div>
			<Button variant="outlined" href={`/suppliers/${location.id}`}>View location</Button>
		</header>
		{#if notice}<p class="notice" role="status">{notice}</p>{/if}
		{#if mutationError}<p class="notice error" role="alert">{mutationError}</p>{/if}
		{#if location.archivedAt}<p class="notice" role="status">
				Archived {formatTimestamp(location.archivedAt)}. This location is unavailable for new
				requests. You can edit or restore it.
			</p>{/if}
		{#if referencesError}
			<p class="notice error" role="alert">Location editing is unavailable: {referencesError}</p>
			<Button onclick={() => referencesReload++}>Retry buildings and categories</Button>
		{:else if referencesLoading}
			<p role="status">Loading buildings and categories for editing…</p>
		{:else}
			{#key `${location.id}:${location.revision}`}<LocationEditor
					{location}
					{buildings}
					{categories}
					onsaved={(saved) => {
						if (page.params.id === saved.id) location = saved;
					}}
				/>{/key}
		{/if}
		{#key location.id}<ClosureManager
				locationId={location.id}
				archived={!!location.archivedAt}
				onchange={refreshLocation}
			/>{/key}
		<Card variant="outlined"
			><div class="panel-body">
				<h2>{location.archivedAt ? 'Restore location' : 'Archive location'}</h2>
				<p>
					{location.archivedAt
						? 'Restore this location to normal discovery and make it available for new requests.'
						: 'Archive this location to remove it from normal discovery and prevent new requests. Historical references are preserved.'}
				</p>
				<div class="form-actions">
					<Button variant="outlined" disabled={busy} onclick={() => (confirmOpen = true)}
						>{location.archivedAt ? 'Restore location' : 'Archive location'}</Button
					>
				</div>
			</div></Card
		>
	{/if}
</section>
<ConfirmDialog
	bind:open={confirmOpen}
	title={location?.archivedAt ? 'Restore location?' : 'Archive location?'}
	label={location?.archivedAt ? 'Restore' : 'Archive'}
	{busy}
	onconfirm={transition}
	><p>{location?.archivedAt ? 'Restore' : 'Archive'} <strong>{location?.name}</strong>?</p>
	<p>
		Any unsaved edits to the location form will be discarded. Save them first if you want to keep
		them.
	</p></ConfirmDialog
>
