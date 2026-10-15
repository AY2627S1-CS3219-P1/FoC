<script lang="ts">
 import { goto } from '$app/navigation';
 import { page } from '$app/state';
 import { Button, Card, LoadingIndicator } from 'm3-svelte';
 import LocationFilters from '$lib/components/location/LocationFilters.svelte';
 import Pagination from '$lib/components/admin/Pagination.svelte';
 import type { Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
 import { locationApi } from '$lib/services';
 import type { LocationPage, LocationQuery } from '$lib/services/location';
 import { queryFromParams, paramsFromQuery } from '$lib/services/location/query';
 import { adminError } from '$lib/services/location-admin/errors';
 let result = $state<LocationPage | null>(null);
 let loading = $state(true), error = $state(''), referenceError = $state('');
 let buildings = $state<Building[]>([]), categories = $state<Category[]>([]);
 let reload = $state(0), references = $state(0);
 const query = $derived(queryFromParams(page.url.searchParams));
 $effect(() => {
  void references;
  let stale = false;
  referenceError = '';
  Promise.all([locationApi.listBuildings(), locationApi.listCategories()]).then(([b, c]) => {
   if (!stale) { buildings = b; categories = c; }
  }).catch(() => { if (!stale) referenceError = 'Building and category filters could not be loaded.'; });
  return () => { stale = true; };
 });
 $effect(() => {
  const current = query; void reload;
  let stale = false; loading = true; error = ''; result = null;
  locationApi.listLocations(current).then((value) => { if (!stale) result = value; })
   .catch((cause) => { if (!stale) error = adminError(cause); })
   .finally(() => { if (!stale) loading = false; });
  return () => { stale = true; };
 });
 function update(changes: Partial<LocationQuery>) {
  const params = paramsFromQuery({ ...query, page: undefined, ...changes });
  void goto(`?${params}`, { keepFocus: true, noScroll: true });
 }
</script>
<svelte:head><title>Manage locations · Friend on Campus</title></svelte:head>
<section class="page-content admin-content" aria-labelledby="locations-title">
 <header class="page-heading"><div><p class="eyebrow">Administration</p><h1 id="locations-title">Locations and suppliers</h1><p>Maintain campus suppliers, facilities and temporary closures.</p></div><Button href="/admin/suppliers/new">Create location</Button></header>
 <Card variant="outlined"><div class="panel-body">
  {#if referenceError}<p class="notice error" role="alert">{referenceError}</p><Button variant="text" onclick={() => references++}>Retry filters</Button>{/if}
  <LocationFilters {query} {buildings} {categories} isAdmin={true} onchange={update} />
 </div></Card>
 <div class="admin-results" aria-busy={loading}>
  {#if error}<p class="notice error" role="alert">{error}</p><Button variant="outlined" onclick={() => reload++}>Retry locations</Button>
  {:else if loading}<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading locations" /><p>Loading locations…</p></div>
  {:else if result}
   <p class="secondary-text" role="status">{result.totalItems} {result.totalItems === 1 ? 'location' : 'locations'}</p>
   {#if result.locations.length === 0}<p class="notice" role="status">No locations on this page. <a href="/admin/suppliers">Reset filters</a></p>{/if}
   <ul class="record-list">{#each result.locations as location (location.id)}
    <li class="record-row"><div><h2><a href={`/admin/suppliers/${location.id}`}>{location.name}</a></h2><p>{location.building?.name ?? 'Unknown building'} · {location.isSupplier ? 'Supplier' : 'Facility'}</p><span class="record-state">{location.archivedAt ? 'Archived' : location.currentDisablement ? 'Temporarily closed' : 'Unarchived'}</span></div><Button variant="outlined" href={`/admin/suppliers/${location.id}`}>Manage</Button></li>
   {/each}</ul>
   <Pagination page={result.page} totalPages={result.totalPages} {loading} onchange={(page) => update({ page })} />
  {/if}
 </div>
</section>
