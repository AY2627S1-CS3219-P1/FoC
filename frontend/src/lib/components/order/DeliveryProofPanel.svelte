<script lang="ts">
	import { Button } from 'm3-svelte';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import type { DeliveryProofRead, DeliveryProofService } from '$lib/services/delivery-proof';
	import DeliveryProofForm from './DeliveryProofForm.svelte';

	let { order, callerId, api, refresh }: {
		order: Order; callerId: string; api: DeliveryProofService; refresh: () => Promise<void>;
	} = $props();
	let proof = $state<DeliveryProofRead | null>(null);
	let error = $state('');
	let loading = $state(false);
	let request = 0;
	let active: AbortController | undefined;
	let refreshTimer: ReturnType<typeof setTimeout> | undefined;
	const participant = $derived(Boolean(callerId) && (order.requesterId === callerId || order.courierId === callerId));
	const canConfirm = $derived(order.courierId === callerId && order.status === OrderStatus.DELIVERING);
	// Ordinary polling must not recreate the form or discard selected File objects.
	const scope = $derived(`${order.id}:${callerId}:${order.status}:${order.revision}:${participant}`);

	async function load(key: string) {
		const id = key.split(':')[0];
		const ticket = ++request;
		active?.abort();
		active = new AbortController();
		clearTimeout(refreshTimer);
		loading = true;
		try {
			const value = await api.read(id, active.signal);
			if (ticket !== request) return;
			proof = value;
			error = '';
			const expiry = Math.min(...value.photos.map((photo) => photo.expiresAt).filter((at) => at > 0));
			if (Number.isFinite(expiry)) refreshTimer = setTimeout(() => void load(key), Math.max(1000, expiry - Date.now() - 30000));
		} catch {
			if (ticket === request) error = 'Could not load delivery photos. Try again before confirming delivery.';
		} finally {
			if (ticket === request) loading = false;
		}
	}

	$effect(() => {
		const key = scope;
		request++;
		proof = null;
		error = '';
		if (key.endsWith(':true')) void load(key);
		return () => { request++; active?.abort(); clearTimeout(refreshTimer); };
	});
</script>

{#if participant}
	<section class="proof-evidence" aria-label="Delivery evidence">
		{#if error}
			<p role="alert">{error}</p>
			<Button variant="outlined" disabled={loading} onclick={() => void load(scope)}>Retry</Button>
		{:else if loading && !proof}<p role="status">Loading delivery photos…</p>{/if}
		{#if proof}
			{#if canConfirm && !proof.photos.length && !proof.outageReason}<DeliveryProofForm orderId={order.id} {api} pending={proof.pending} onDelivered={refresh} />
			{:else if canConfirm}<p role="status">Delivery confirmed. Refresh to see the latest order.</p><Button variant="outlined" onclick={refresh}>Refresh order</Button>{/if}
			{#if proof.photos.length > 0}
				<header><h2>Delivery proof</h2></header>
				<ul aria-label="Saved delivery proof photos">
					{#each proof.photos as photo, index (photo.id)}
						<li>
							<a href={photo.url} target="_blank" rel="noreferrer">
								{#if ['image/jpeg', 'image/png', 'image/webp'].includes(photo.contentType)}
									<span class="photo-preview"><img src={photo.url} alt={`Delivery proof photo ${index + 1}`} loading="lazy" referrerpolicy="no-referrer" /></span>
								{/if}
								<span class="photo-link-text">Photo {index + 1}</span>
							</a>
						</li>
					{/each}
				</ul>
				<Button variant="text" disabled={loading} onclick={() => void load(scope)}>Refresh photos</Button>
			{/if}
			{#if proof.outageReason}<p role="status">Delivered without photos because photo uploads were unavailable.</p>{/if}
		{/if}
	</section>
{/if}

<style>
	.proof-evidence { min-width: 0; display: grid; gap: 1rem; overflow-wrap: anywhere; line-height: 1.5; }
	.proof-evidence h2, .proof-evidence p { margin: 0; }
	.proof-evidence h2 { font-size: 1.35rem; }
	.proof-evidence ul { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr)); gap: 1rem; padding: 0; margin: 0; list-style: none; }
	.proof-evidence li { display: grid; align-content: start; gap: .75rem; min-width: 0; padding: .75rem; border: 1px solid var(--m3c-outline-variant); border-radius: .75rem; }
	.photo-preview { display: grid; place-items: center; aspect-ratio: 4 / 3; background: var(--m3c-surface-container); border-radius: .5rem; overflow: hidden; }
	.proof-evidence img { width: 100%; height: 100%; min-height: 0; object-fit: contain; }
	.proof-evidence li > a { display: grid; gap: .75rem; }
	.photo-link-text { line-height: 1.5; padding-block: .4rem; }
	.proof-evidence > :global(button) { justify-self: start; height: auto; min-height: 2.75rem; white-space: normal; padding-block: .65rem; }
	.proof-evidence :global(:focus-visible) { outline: 3px solid var(--m3c-primary, #1a5b9b); outline-offset: 3px; }
</style>
