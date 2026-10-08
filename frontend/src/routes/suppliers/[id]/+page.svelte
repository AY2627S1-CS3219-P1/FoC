<script lang="ts">
	import { page } from '$app/state';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import LocationTags from '$lib/components/location/LocationTags.svelte';
	import Notice from '$lib/components/location/Notice.svelte';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { authService, locationApi } from '$lib/services';
	import { describeLocationError } from '$lib/services/location/errors';
	import { formatOpeningHours } from '$lib/services/location/format';

	let location = $state<Location | null>(null);
	let error = $state<string | null>(null);

	$effect(() => {
		const id = page.params.id ?? '';
		let stale = false;
		location = null;
		error = null;
		locationApi
			.getLocation(id)
			.then((found) => !stale && (location = found))
			.catch((e) => !stale && (error = describeLocationError(e)));
		return () => (stale = true);
	});

	const dateFormat = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });
</script>

<svelte:head><title>{location?.name ?? 'Location'} · Friend on Campus</title></svelte:head>

<article class="page-content">
	<div><Button variant="text" href="/suppliers">← All locations</Button></div>

	{#if error}
		<Notice tone="error">
			{error}
			{#if !authService.isAuthenticated}<a href="/login">Log in</a>{/if}
		</Notice>
	{:else if !location}
		<div class="loading-state">
			<LoadingIndicator aria-label="Loading location" />
			<p>Loading…</p>
		</div>
	{:else}
		{#if location.archivedAt}
			<Notice tone="warn">
				Archived on {dateFormat.format(timestampDate(location.archivedAt))}. It can no longer be
				selected for new requests.
			</Notice>
		{/if}
		{#if location.currentDisablement}
			<Notice tone="warn">
				Temporarily closed: {location.currentDisablement.reason}
				{#if location.currentDisablement.endsAt}
					(until {dateFormat.format(timestampDate(location.currentDisablement.endsAt))})
				{/if}
			</Notice>
		{/if}

		<header class="page-heading">
			<div>
				<p class="eyebrow">Location</p>
				<h1>{location.name}</h1>
				<LocationTags {location} />
			</div>
		</header>

		<Card variant="outlined">
			<dl class="panel-body">
				<div>
					<dt>Building</dt>
					<dd>{location.building?.name ?? 'Unknown'}</dd>
				</div>

				{#if location.floor}
					<div>
						<dt>Floor</dt>
						<dd>{location.floor}</dd>
					</div>
				{/if}

				<div>
					<dt>Opening hours</dt>
					<dd>{formatOpeningHours(location.opensAt, location.closesAt)}</dd>
				</div>

				{#if location.contact}
					<div>
						<dt>Contact</dt>
						<dd>{location.contact}</dd>
					</div>
				{/if}

				{#if location.details}
					<div>
						<dt>Details</dt>
						<dd class="prose">{location.details}</dd>
					</div>
				{/if}

				{#if location.coordinates}
					<div>
						<dt>Coordinates</dt>
						<dd>
							{location.coordinates.latitude.toFixed(5)}, {location.coordinates.longitude.toFixed(5)}
						</dd>
					</div>
				{/if}

				{#if location.updatedAt}
					<div>
						<dt>Last updated</dt>
						<dd>{dateFormat.format(timestampDate(location.updatedAt))}</dd>
					</div>
				{/if}
			</dl>
		</Card>
	{/if}
</article>

<style>
	dl {
		margin: 0;
	}

	dl > div {
		display: grid;
		grid-template-columns: 9rem 1fr;
		gap: 1rem;
	}

	dt {
		color: var(--m3c-on-surface-variant);
		font-size: 0.875rem;
		font-weight: 600;
	}

	dd {
		margin: 0;
		overflow-wrap: anywhere;
	}

	.prose {
		white-space: pre-line;
	}

	@media (max-width: 36rem) {
		dl > div {
			grid-template-columns: 1fr;
			gap: 0.2rem;
		}
	}
</style>
