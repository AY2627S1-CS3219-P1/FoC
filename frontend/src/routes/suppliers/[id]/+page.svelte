<script lang="ts">
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import LocationDetails from '$lib/components/location/LocationDetails.svelte';
	import Notice from '$lib/components/location/Notice.svelte';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { authService, locationApi } from '$lib/services';
	import { describeLocationError } from '$lib/services/location/errors';

	let location = $state<Location | null>(null);
	let error = $state<string | null>(null);
	let reload = $state(0);

	$effect(() => {
		const id = page.params.id ?? '';
		void reload;
		let stale = false;
		location = null;
		error = null;
		locationApi
			.getLocation(id)
			.then((found) => !stale && (location = found))
			.catch((e) => !stale && (error = describeLocationError(e)));
		return () => (stale = true);
	});
</script>

<svelte:head><title>{location?.name ?? 'Location'} · Friend on Campus</title></svelte:head>

<article class="page-content">
	<div><Button variant="text" href="/suppliers">All suppliers</Button></div>

	{#if error}
		<Notice tone="error">
			{error}
			<Button variant="text" onclick={() => reload++}>Retry location</Button>
			{#if !authService.isAuthenticated}<a href="/login">Log in</a>{/if}
		</Notice>
	{:else if !location}
		<div class="loading-state">
			<LoadingIndicator aria-label="Loading location" />
			<p>Loading…</p>
		</div>
	{:else}
		<Card variant="outlined">
			<div class="panel-body">
				<LocationDetails {location}>
					{#snippet actions()}
						{#if location && !location.archivedAt}
							<Button href={'/orders/new?pickup=' + encodeURIComponent(location.id)}>Make a request</Button>
						{/if}
					{/snippet}
				</LocationDetails>
			</div>
		</Card>
	{/if}
</article>
