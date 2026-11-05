<script lang="ts">
	import type { Snippet } from 'svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import CampusMap from '$lib/components/map/CampusMap.svelte';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { formatOpeningHours } from '$lib/services/location/format';
	import LocationTags from './LocationTags.svelte';
	import Notice from './Notice.svelte';

	let {
		location,
		headingLevel = 1,
		actions
	}: {
		location: Location;
		/** 2 when shown as a panel beside a list that has its own h1. */
		headingLevel?: 1 | 2;
		actions?: Snippet;
	} = $props();

	const dateFormat = new Intl.DateTimeFormat('en-SG', {
		dateStyle: 'medium',
		timeStyle: 'short',
		timeZone: 'Asia/Singapore'
	});
</script>

<div class="details">
	{#if location.archivedAt}
		<Notice tone="warn">
			Archived on {dateFormat.format(timestampDate(location.archivedAt))}. It can no longer be selected
			for new requests.
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

	<header>
		<svelte:element this={'h' + headingLevel} class="name">{location.name}</svelte:element>
		<p class="where">{location.building?.name ?? 'Unknown building'}{location.floor ? ', ' + location.floor : ''}</p>
		<LocationTags {location} />
	</header>

	{#if location.coordinates}
		<div class="map">
			<CampusMap
				label={'Map showing ' + location.name}
				markers={[
					{
						id: location.id,
						latitude: location.coordinates.latitude,
						longitude: location.coordinates.longitude,
						label: location.name,
						kind: 'supplier'
					}
				]}
				selectedId={location.id}
			/>
		</div>
	{/if}

	{#if actions}<div class="actions">{@render actions()}</div>{/if}

	<dl>
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
		{#if location.updatedAt}
			<div>
				<dt>Last updated</dt>
				<dd>{dateFormat.format(timestampDate(location.updatedAt))}</dd>
			</div>
		{/if}
	</dl>
</div>

<style>
	.details {
		display: grid;
		gap: 1rem;
		min-width: 0;
	}

	header {
		display: grid;
		gap: 0.35rem;
	}

	.name {
		margin: 0;
		font-size: clamp(1.4rem, 3vw, 1.9rem);
		line-height: 1.2;
	}

	.where {
		margin: 0;
		color: var(--m3c-on-surface-variant);
	}

	.map {
		height: 13rem;
	}

	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	dl {
		display: grid;
		gap: 0.75rem;
		margin: 0;
	}

	dl > div {
		display: grid;
		grid-template-columns: 8rem 1fr;
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
