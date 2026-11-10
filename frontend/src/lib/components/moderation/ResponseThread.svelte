<script lang="ts">
	import type { EvidenceRecord } from '$lib/services/moderation';
	import EvidenceGallery from './EvidenceGallery.svelte';

	type Response = {
		id: string;
		responderId: string;
		responderName: string;
		body: string;
		createdAt: Date;
		evidence: EvidenceRecord[];
	};

	// Responses to a report or appeal, oldest first as the server returns them.
	let {
		responses,
		refresh,
		callerId = ''
	}: {
		responses: Response[];
		refresh: (id: string) => Promise<EvidenceRecord>;
		callerId?: string;
	} = $props();

	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });
</script>

<section class="thread" aria-labelledby="responses-title">
	<h2 id="responses-title">Responses</h2>
	{#if responses.length === 0}
		<p class="secondary-text">No responses yet.</p>
	{:else}
		<ol>
			{#each responses as response (response.id)}
				<li>
					<p class="meta">
						<strong>{response.responderId === callerId ? 'You' : response.responderName || 'A participant'}</strong>
						· {date.format(response.createdAt)}
					</p>
					<p class="body">{response.body}</p>
					<EvidenceGallery evidence={response.evidence} {refresh} label="Evidence attached to this response" />
				</li>
			{/each}
		</ol>
	{/if}
</section>

<style>
	.thread { display: grid; gap: 0.5rem; }
	h2 { margin: 0; font-size: 1.1rem; }
	ol { display: grid; gap: 0.75rem; margin: 0; padding: 0; list-style: none; }
	li {
		display: grid;
		gap: 0.4rem;
		padding: 0.75rem 1rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
	}
	.meta { margin: 0; font-size: 0.875rem; color: var(--m3c-on-surface-variant); }
	.body { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
