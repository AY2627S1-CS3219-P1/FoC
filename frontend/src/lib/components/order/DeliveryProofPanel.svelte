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
			if (ticket === request) error = 'Proof status could not be refreshed. Retry before starting a new confirmation. Delivery has not been inferred from this failure.';
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
			<Button variant="outlined" disabled={loading} onclick={() => void load(scope)}>Retry proof status</Button>
		{:else if loading && !proof}<p role="status">Checking delivery proof status…</p>{/if}
		{#if proof}
			{#if canConfirm && !proof.photos.length && !proof.outageReason}<DeliveryProofForm orderId={order.id} {api} pending={proof.pending} onDelivered={refresh} />
			{:else if canConfirm}<p role="status">The server accepted delivery. Refresh the order to see its current status.</p><Button variant="outlined" onclick={refresh}>Refresh accepted delivery</Button>{/if}
			{#if proof.photos.length > 0}
				<h2>Delivery proof</h2>
				<ul aria-label="Saved delivery proof photos">
					{#each proof.photos as photo, index (photo.id)}
						<li>
							{#if ['image/jpeg', 'image/png', 'image/webp'].includes(photo.contentType)}
								<img src={photo.url} alt={`Delivery proof photo ${index + 1}`} loading="lazy" referrerpolicy="no-referrer" />
							{/if}
							<a href={photo.url} target="_blank" rel="noreferrer">Open or download proof photo {index + 1}</a>
						</li>
					{/each}
				</ul>
				<Button variant="text" disabled={loading} onclick={() => void load(scope)}>Refresh proof links</Button>
			{/if}
			{#if proof.outageReason}<p role="status">Delivered without photos. Server-confirmed upload outage: {proof.outageReason}</p>{/if}
		{/if}
	</section>
{/if}

<style>
	.proof-evidence { min-width: 0; display: grid; gap: 1rem; overflow-wrap: anywhere; }
	.proof-evidence ul { display: grid; gap: 1rem; padding-inline-start: 1.25rem; }
	.proof-evidence li { display: grid; gap: .5rem; min-width: 0; }
	.proof-evidence img { max-width: 100%; max-height: 24rem; object-fit: contain; }
	.proof-evidence :global(:focus-visible) { outline: 3px solid var(--m3-scheme-primary, #1a5b9b); outline-offset: 3px; }
</style>
