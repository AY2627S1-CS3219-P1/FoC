<script lang="ts">
	import { page } from '$app/state';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
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

<article>
	<a class="back" href="/suppliers">← All locations</a>

	{#if error}
		<Notice tone="error">
			{error}
			{#if !authService.isAuthenticated}<a href="/login">Log in</a>{/if}
		</Notice>
	{:else if !location}
		<Notice>Loading…</Notice>
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

		<h1>{location.name}</h1>
		<LocationTags {location} />

		<dl>
			<dt>Building</dt>
			<dd>{location.building?.name ?? 'Unknown'}</dd>

			{#if location.floor}
				<dt>Floor</dt>
				<dd>{location.floor}</dd>
			{/if}

			<dt>Opening hours</dt>
			<dd>{formatOpeningHours(location.opensAt, location.closesAt)}</dd>

			{#if location.contact}
				<dt>Contact</dt>
				<dd>{location.contact}</dd>
			{/if}

			{#if location.details}
				<dt>Details</dt>
				<dd class="prose">{location.details}</dd>
			{/if}

			{#if location.coordinates}
				<dt>Coordinates</dt>
				<dd>
					{location.coordinates.latitude.toFixed(5)}, {location.coordinates.longitude.toFixed(5)}
				</dd>
			{/if}

			{#if location.updatedAt}
				<dt>Last updated</dt>
				<dd>{dateFormat.format(timestampDate(location.updatedAt))}</dd>
			{/if}
		</dl>
	{/if}
</article>

<style>
	article {
		max-width: 42rem;
	}

	.back {
		display: inline-block;
		margin-bottom: 1rem;
	}

	h1 {
		font-size: clamp(1.75rem, 4vw, 2.5rem);
		margin-bottom: 0.5rem;
	}

	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.6rem 1.25rem;
		margin-top: 1.5rem;
	}

	dt {
		color: #527099;
		font-weight: 600;
	}

	dd {
		margin: 0;
	}

	.prose {
		white-space: pre-line;
	}

	@media (max-width: 40rem) {
		dl {
			grid-template-columns: 1fr;
			gap: 0.2rem;
		}

		dd {
			margin-bottom: 0.6rem;
		}
	}
</style>
