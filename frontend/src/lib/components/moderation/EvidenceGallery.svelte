<script lang="ts">
	import { Icon } from 'm3-svelte';
	import brokenImageIcon from '@ktibow/iconset-material-symbols/broken-image-outline-rounded';
	import type { EvidenceRecord } from '$lib/services/moderation';

	// Evidence images on a report, appeal or response. Their URLs are short-lived,
	// so an expired or broken image asks the owning service for a fresh URL.
	let {
		evidence,
		refresh,
		label = 'Evidence'
	}: {
		evidence: EvidenceRecord[];
		refresh: (id: string) => Promise<EvidenceRecord>;
		label?: string;
	} = $props();

	let fresh = $state<Record<string, EvidenceRecord>>({});
	let broken = $state<Record<string, boolean>>({});

	const current = (item: EvidenceRecord) => fresh[item.id] ?? item;
	const expired = (item: EvidenceRecord) => item.expiresAt > 0 && item.expiresAt <= Date.now();

	async function renew(id: string): Promise<EvidenceRecord | null> {
		try {
			const renewed = await refresh(id);
			fresh[id] = renewed;
			broken[id] = false;
			return renewed;
		} catch {
			broken[id] = true;
			return null;
		}
	}

	async function open(event: MouseEvent, item: EvidenceRecord) {
		if (!expired(current(item))) return;
		event.preventDefault();
		// Open the tab synchronously so the browser does not block it as a popup.
		const tab = window.open('', '_blank', 'noreferrer');
		const renewed = await renew(item.id);
		if (tab && renewed) tab.location.href = renewed.url;
		else tab?.close();
	}
</script>

{#if evidence.length > 0}
	<ul class="evidence" aria-label={label}>
		{#each evidence as item, index (item.id)}
			{@const shown = current(item)}
			<li>
				<a
					href={shown.url}
					target="_blank"
					rel="noreferrer"
					title="Open image {index + 1} in a new tab"
					onclick={(event) => void open(event, item)}
				>
					{#if broken[item.id]}
						<span class="placeholder" role="img" aria-label={`Image ${index + 1} is unavailable`}>
							<Icon icon={brokenImageIcon} size={24} aria-hidden="true" />
						</span>
					{:else}
						<img
							src={shown.url}
							alt={`Evidence image ${index + 1}`}
							loading="lazy"
							referrerpolicy="no-referrer"
							onerror={() => {
								// Retry once with a fresh URL; a second failure stays broken.
								if (fresh[item.id]) broken[item.id] = true;
								else void renew(item.id);
							}}
						/>
					{/if}
				</a>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.evidence { display: flex; flex-wrap: wrap; gap: 0.5rem; margin: 0; padding: 0; list-style: none; }
	a {
		display: block;
		width: 5rem;
		height: 5rem;
		border-radius: 0.5rem;
		overflow: hidden;
		border: 1px solid var(--m3c-outline-variant);
		background: var(--m3c-surface-container);
	}
	a:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	img { width: 100%; height: 100%; object-fit: cover; display: block; }
	.placeholder { display: grid; place-items: center; width: 100%; height: 100%; color: var(--m3c-on-surface-variant); }
</style>
