<script lang="ts">
	import { Button, Card } from 'm3-svelte';
	import { untrack } from 'svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { z } from 'zod';
	import {
		DeliveryProofController,
		type ProofSnapshot
	} from '$lib/features/delivery-proof/controller';

	import type { DeliveryProofAPI } from '$lib/services/delivery-proof/api';

	// Mounted by authenticated caller/order, only for the assigned courier
	// while Delivering. Durable pending input survives navigation and reload.
	let { orderId, api, onDelivered, pending }: {
		orderId: string;
		api: DeliveryProofAPI;
		pending?: Parameters<DeliveryProofAPI['confirmDelivery']>[0];
		onDelivered: () => Promise<void>;
	} = $props();
	let view = $state<ProofSnapshot>({
		files: [], unattended: false, outcome: 'Not Submitted', message: '', locked: false, busy: false
	});
	let controller: DeliveryProofController | undefined = $state();
	let refreshError = $state('');
	let statusElement: HTMLParagraphElement | undefined = $state();
	let fileInput: HTMLInputElement | undefined = $state();
	const schema = z.object({ unattended: z.boolean().default(false) });

	$effect(() => {
		const instance = new DeliveryProofController(orderId, api, (snapshot) => { view = snapshot; }, undefined, untrack(() => pending));
		controller = instance;
		view = instance.snapshot();
		return () => instance.dispose();
	});

	async function refreshAfterSuccess() {
		if (view.outcome !== 'Succeeded') return;
		try {
			await onDelivered();
		} catch {
			refreshError = 'Delivery succeeded, but the order display could not refresh. Refresh the order to see its current status.';
		}
	}

	const confirmation = superForm(defaults({ unattended: untrack(() => pending?.unattended ?? false) }, zod4(schema)), {
		SPA: true,
		validators: zod4Client(schema),
		multipleSubmits: 'prevent',
		resetForm: false,
		async onUpdate({ form }) {
			if (!form.valid || !controller) return;
			controller.setUnattended(form.data.unattended);
			await controller.confirm();
			statusElement?.focus();
			await refreshAfterSuccess();
		}
	});
	const { form, enhance } = confirmation;

	async function confirmOutage() {
		if (!controller) return;
		controller.setUnattended($form.unattended);
		await controller.confirm(true);
		statusElement?.focus();
		await refreshAfterSuccess();
	}
</script>

<section aria-label="Delivery proof" class="proof-panel">
	<Card variant="outlined">
		<div class="proof-body">
			<h2>Confirm delivery</h2>
			<p>Select 1–5 proof photos. Each photo must be JPEG, PNG, WebP, HEIC or HEIF and at most 10,000,000 bytes.</p>
			<p>Upload and check photos before submitting confirmation. Only the server-received confirmation reserves the original deadline, not photo selection or upload alone.</p>
			{#if pending && view.locked}
				<p>Saved submission: {pending.proofIds.length} previously uploaded photo(s). {pending.withoutProof ? 'An upload-outage exception was requested.' : ''}</p>
			{/if}
			<label for="delivery-proof-files">Proof photos</label>
			<input
				bind:this={fileInput}
				id="delivery-proof-files"
				type="file"
				accept="image/jpeg,image/png,image/webp,image/heic,image/heif"
				multiple
				disabled={view.locked}
				onchange={(event) => {
					controller?.addFiles(Array.from(event.currentTarget.files ?? []));
					event.currentTarget.value = '';
				}}
			/>
			<ul aria-label="Selected proof photos">
				{#each view.files as row (row.id)}
					<li>
						<strong>{row.file.name}</strong>
						<span>{row.outcome}</span>
						{#if row.message}<p>{row.message}</p>{/if}
						<Button type="button" variant="text" disabled={view.locked} onclick={async () => {
							await controller?.removeFile(row.id);
							fileInput?.focus();
						}}>Remove {row.file.name}</Button>
					</li>
				{/each}
			</ul>
			<Button type="button" variant="outlined" disabled={view.locked || view.files.length === 0} onclick={() => controller?.upload()}>
				Upload or retry photos
			</Button>
			<form use:enhance>
				<label class="unattended">
					<input type="checkbox" name="unattended" bind:checked={$form.unattended} disabled={view.locked} />
					The requester was unreachable. I left the item without a handover.
				</label>
				<Button type="submit" disabled={view.busy || view.outcome === 'Succeeded'}>
					{view.locked && !view.busy ? 'Retry saved confirmation' : 'Confirm delivery'}
				</Button>
			</form>
			<details>
				<summary>Photo uploads unavailable?</summary>
				<p>The server must confirm a storage outage. A failed upload or lost connection alone does not permit delivery without proof. This does not extend the delivery deadline.</p>
				<Button type="button" variant="text" disabled={view.locked} onclick={confirmOutage}>
					Request outage exception and confirm without photos
				</Button>
			</details>
			<p bind:this={statusElement} tabindex="-1" role="status" aria-live="polite" class="status">
				{view.outcome}: {view.message}
			</p>
			{#if refreshError}<p role="alert">{refreshError}</p>{/if}
		</div>
	</Card>
</section>

<style>
	.proof-panel { min-width: 0; max-width: 100%; }
	.proof-body { display: grid; gap: 1rem; padding: 1rem; min-width: 0; overflow-wrap: anywhere; }
	.proof-body h2, .proof-body p { margin: 0; }
	.proof-body input[type='file'] { width: 100%; min-width: 0; max-width: 100%; }
	.proof-body ul { margin: 0; padding-inline-start: 1.25rem; }
	.proof-body li { margin-bottom: 1rem; display: grid; gap: .5rem; }
	.proof-body form { display: grid; gap: 1rem; min-width: 0; }
	.unattended { display: flex; align-items: flex-start; gap: .5rem; }
	.proof-body :global(button) { max-width: 100%; height: auto; min-height: 2.75rem; white-space: normal; }
	.proof-body :global(:focus-visible), .status:focus { outline: 3px solid var(--m3-scheme-primary, #1a5b9b); outline-offset: 3px; }
	.proof-body details { min-width: 0; }
	.proof-body details p { margin-block: .75rem; }
</style>
