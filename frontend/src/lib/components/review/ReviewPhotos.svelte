<script lang="ts">
	import SignedImage from '$lib/components/image/SignedImage.svelte';
	import { authService, reviewApi } from '$lib/services';
	import { resolve } from '$app/paths';
	import { Icon } from 'm3-svelte';
	import brokenImageIcon from '@ktibow/iconset-material-symbols/broken-image-outline-rounded';
	import type { ReviewPhotoRecord } from '$lib/services/review/reviewApi';

	// Thumbnails of the photos attached to one review. The URLs are short-lived
	// pre-signed grants minted per response, so this never caches them.
	let { photos }: { photos: ReviewPhotoRecord[] } = $props();
</script>

{#if photos.length > 0}
	<ul aria-label="Review photos">
		{#each photos as photo, index (photo.id)}
			<li>
				<a href={resolve('/reviews/photos/[photoId]', { photoId: photo.id })} target="_blank" rel="noreferrer" title="Open photo {index + 1} in a new tab">
					<SignedImage
						objectId={photo.id}
						initialGrant={photo}
						refresh={(id, signal) => reviewApi.getPhotoDownload(id, signal)}
						sessionKey={`${authService.user?.id}:${authService.user?.role}`}
						alt={`Review photo ${index + 1}`}
					>
						{#snippet fallback()}
							<Icon icon={brokenImageIcon} size={24} aria-hidden="true" />
						{/snippet}
					</SignedImage>
				</a>
			</li>
		{/each}
	</ul>
{/if}

<style>
	ul { display: flex; flex-wrap: wrap; gap: 0.5rem; margin: 0; padding: 0; list-style: none; }
	a { display: block; width: 4.5rem; height: 4.5rem; border-radius: 0.5rem; overflow: hidden; border: 1px solid var(--m3c-outline-variant); background: var(--m3c-surface-container); }
	a:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
</style>
