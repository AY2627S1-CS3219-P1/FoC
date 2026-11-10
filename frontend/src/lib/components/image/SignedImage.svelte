<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { ImageGrant, RefreshImageGrant } from './types';

	type Props = {
		/** Stable application ID, independent of the signed URL. */
		objectId: string;
		/** Use this URL first; refresh on a load error rather than an expiry timer. */
		initialGrant?: ImageGrant;
		/** The parent supplies the domain API call, including authentication. */
		refresh: RefreshImageGrant;
		/** Stable caller identity/role; access-token rotation must not reset retries. */
		sessionKey?: string;
		alt: string;
		loading?: 'lazy' | 'eager';
		fit?: 'cover' | 'contain';
		fallback?: Snippet;
	};

	let {
		objectId,
		initialGrant,
		refresh,
		sessionKey,
		alt,
		loading = 'lazy',
		fit = 'cover',
		fallback
	}: Props = $props();

	type ImageDisplayState = {
		url?: string;
		failed: boolean;
		retryWithFreshGrant: () => void;
	};

	// Rendering has three states: an image URL, a loading placeholder (no URL),
	// or a terminal fallback (failed). Each update creates a new state object.
	let displayState = $state<ImageDisplayState>();

	$effect(() => {
		const imageId = objectId;
		const initial = initialGrant;
		const requestGrant = refresh;

		// Read this prop so a user/role change also restarts the effect, even if
		// the image ID and initial URL stay the same.
		void sessionKey;

		// This effect owns one refresh budget and request. Cleanup aborts it on
		// prop changes or unmount; late results cannot update the next image.
		const controller = new AbortController();
		let hasRequestedFreshGrant = false;
		const imageState: ImageDisplayState = {
			url: initial?.url,
			failed: false,
			retryWithFreshGrant
		};
		displayState = imageState;

		async function loadFreshGrant() {
			try {
				const grant = await requestGrant(imageId, controller.signal);
				if (controller.signal.aborted) return;
				if (!grant.url) throw new Error('Missing image URL');

				imageState.url = grant.url;
				displayState = { ...imageState };
			} catch {
				if (controller.signal.aborted) return;
				displayState = { ...imageState, failed: true };
			}
		}

		function retryWithFreshGrant() {
			if (controller.signal.aborted) return;

			// A failed replacement image ends at the fallback, never another RPC.
			if (hasRequestedFreshGrant) {
				displayState = { ...imageState, failed: true };
				return;
			}
			hasRequestedFreshGrant = true;

			// Remove the old <img> while fetching. Its remaining error events must
			// not consume the retry intended for the replacement image.
			displayState = { ...imageState, url: undefined };

			// Defer the callback so its reactive reads are outside this effect's
			// dependency tracking. Both thrown errors and rejected requests are
			// handled by loadFreshGrant.
			void Promise.resolve().then(loadFreshGrant);
		}

		// Viewers may have no initial grant. Their first fetch uses the same
		// single-request budget as recovery from a failed thumbnail URL.
		if (!initial?.url) retryWithFreshGrant();

		return () => controller.abort();
	});
</script>

{#if displayState?.failed}
	<span class="fallback" role="img" aria-label={`${alt} is unavailable`}>
		{#if fallback}
			{@render fallback()}
		{:else}
			Photo unavailable
		{/if}
	</span>
{:else if displayState?.url}
	<!-- Recreate the image for each state update. The captured state lets us
	     ignore an error delivered by an image that has already been replaced. -->
	{#key displayState}
		{@const renderedState = displayState}
		<img
			src={renderedState.url}
			{alt}
			{loading}
			referrerpolicy="no-referrer"
			onerror={() => {
				if (displayState === renderedState) {
					renderedState.retryWithFreshGrant();
				}
			}}
			style:object-fit={fit}
		/>
	{/key}
{:else}
	<span class="fallback" role="status" aria-label="Loading photo"></span>
{/if}

<style>
	img {
		width: 100%;
		height: 100%;
		display: block;
	}

	.fallback {
		display: grid;
		place-items: center;
		width: 100%;
		height: 100%;
		color: var(--m3c-on-surface-variant);
	}
</style>
