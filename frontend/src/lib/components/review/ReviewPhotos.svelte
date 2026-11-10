<script lang="ts">
	import { Icon } from 'm3-svelte';
	import brokenImageIcon from '@ktibow/iconset-material-symbols/broken-image-outline-rounded';
	import type { ReviewPhotoRecord } from '$lib/services/review/reviewApi';

	// Thumbnails of the photos attached to one review. The URLs are short-lived
	// pre-signed grants minted per response, so this never caches them.
	let { photos }: { photos: ReviewPhotoRecord[] } = $props();
	let broken = $state(new Set<string>());
</script>

{#if photos.length > 0}
	<ul aria-label="Review photos">
		{#each photos as photo, index (photo.id)}
			<li>
				<a href={photo.url} target="_blank" rel="noreferrer" title="Open photo {index + 1} in a new tab">
					{#if broken.has(photo.id)}
						<span class="placeholder" role="img" aria-label={`Review photo ${index + 1} is unavailable`}>
							<Icon icon={brokenImageIcon} size={24} aria-hidden="true" />
						</span>
					{:else}
						<img
							src={photo.url}
							alt={`Review photo ${index + 1}`}
							loading="lazy"
							referrerpolicy="no-referrer"
							onerror={() => {
								broken.add(photo.id);
								broken = new Set(broken);
							}}
						/>
					{/if}
				</a>
			</li>
		{/each}
	</ul>
{/if}

<style>
	ul { display: flex; flex-wrap: wrap; gap: 0.5rem; margin: 0; padding: 0; list-style: none; }
	a { display: block; width: 4.5rem; height: 4.5rem; border-radius: 0.5rem; overflow: hidden; border: 1px solid var(--m3c-outline-variant); background: var(--m3c-surface-container); }
	a:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	img { width: 100%; height: 100%; object-fit: cover; display: block; }
	.placeholder { display: grid; place-items: center; width: 100%; height: 100%; color: var(--m3c-on-surface-variant); }
</style>
