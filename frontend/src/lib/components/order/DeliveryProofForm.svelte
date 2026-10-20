<script lang="ts">
	import { Button, Card } from 'm3-svelte';
	import { untrack } from 'svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { z } from 'zod';
	import {
		DeliveryProofController,
		type DeliveryProofAPI,
		type ProofSnapshot
	} from '$lib/features/delivery-proof/controller';

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
	<Card variant="outlined" style="padding: 0;">
		<div class="proof-body">
			<header>
				<h2>Confirm delivery</h2>
				<p>Upload and check your photos, then confirm delivery.</p>
			</header>
			<p class="deadline-note">Only the server-received confirmation reserves the original deadline, not photo selection or upload alone.</p>
			{#if pending && view.locked}
				<p>Saved submission: {pending.proofIds.length} previously uploaded photo(s). {pending.withoutProof ? 'An upload-outage exception was requested.' : ''}</p>
			{/if}
			<div class="photo-selection">
				<div class="selection-heading"><h3>Proof photos</h3><span>{pending && view.locked ? `${pending.proofIds.length} saved` : `${view.files.length} / 5 selected`}</span></div>
				<p id="proof-file-help" class="file-help">Select 1–5 photos. JPEG, PNG, WebP, HEIC or HEIF. Up to 10 MB (10,000,000 bytes) each.</p>
				<div class="file-picker">
					<input
						bind:this={fileInput}
						id="delivery-proof-files"
						aria-describedby="proof-file-help"
						class="file-input"
						type="file"
						accept="image/jpeg,image/png,image/webp,image/heic,image/heif"
						multiple
						disabled={view.locked}
						onchange={(event) => {
							controller?.addFiles(Array.from(event.currentTarget.files ?? []));
							event.currentTarget.value = '';
						}}
					/>
					<label for="delivery-proof-files" class="choose-photos" class:disabled={view.locked}>Choose photos</label>
				</div>
				<ul aria-label="Selected proof photos">
					{#each view.files as row (row.id)}
						<li>
							<div class="file-summary"><strong>{row.file.name}</strong><span class="file-outcome">{row.outcome}</span></div>
							<div class="remove-action"><Button type="button" variant="text" aria-label={`Remove ${row.file.name}`} disabled={view.locked} onclick={async () => {
								await controller?.removeFile(row.id);
								fileInput?.focus();
							}}>Remove</Button></div>
							{#if row.message}<p class="file-message">{row.message}</p>{/if}
						</li>
					{/each}
				</ul>
				<Button type="button" variant="outlined" disabled={view.locked || view.files.length === 0} onclick={() => controller?.upload()}>
					Upload or retry photos
				</Button>
			</div>
			<form use:enhance>
				<label class="unattended">
					<input type="checkbox" name="unattended" bind:checked={$form.unattended} disabled={view.locked} />
					The requester was unreachable. I left the item without a handover.
				</label>
				<div class="confirm-action"><Button type="submit" disabled={view.busy || view.outcome === 'Succeeded'}>
					{view.locked && !view.busy ? 'Retry saved confirmation' : 'Confirm delivery'}
				</Button></div>
			</form>
			<details>
				<summary>Photo uploads unavailable?</summary>
				<p>The server must confirm a storage outage. A failed upload or lost connection alone does not permit delivery without proof. This does not extend the delivery deadline. Requesting the exception also submits delivery confirmation without photos.</p>
				<Button type="button" variant="text" disabled={view.locked} onclick={confirmOutage}>
					Request outage exception
				</Button>
			</details>
			<p bind:this={statusElement} tabindex="-1" role="status" aria-live="polite" class="status">
				<strong>{view.outcome}</strong>{#if view.message}<span>{view.message}</span>{/if}
			</p>
			{#if refreshError}<p role="alert">{refreshError}</p>{/if}
		</div>
	</Card>
</section>

<style>
	.proof-panel { min-width: 0; max-width: 100%; }
	.proof-body { display: grid; gap: 1.25rem; padding: 1.25rem; min-width: 0; overflow-wrap: anywhere; line-height: 1.5; }
	.proof-body h2, .proof-body h3, .proof-body p { margin: 0; }
	.proof-body h2 { font-size: 1.35rem; line-height: 1.3; }
	.proof-body h3 { font-size: 1rem; }
	header { display: grid; gap: .4rem; }
	header p, .file-help { color: var(--m3c-on-surface-variant); }
	.deadline-note { border-inline-start: 3px solid var(--m3c-primary); padding-inline-start: .75rem; font-size: .9rem; }
	.photo-selection { display: grid; gap: .75rem; min-width: 0; }
	.selection-heading { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: .25rem .75rem; }
	.selection-heading span, .file-help, .file-outcome { font-size: .875rem; }
	.file-picker { position: relative; justify-self: start; }
	.file-input { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
	.choose-photos { display: inline-flex; align-items: center; justify-content: center; min-height: 2.75rem; padding: .5rem 1rem; border: 1px solid var(--m3c-outline); border-radius: 2rem; color: var(--m3c-primary); cursor: pointer; font-weight: 600; }
	.choose-photos.disabled { opacity: .5; cursor: default; }
	.file-input:focus-visible + .choose-photos { outline: 3px solid var(--m3c-primary); outline-offset: 3px; }
	.proof-body ul { list-style: none; margin: 0; padding: 0; display: grid; gap: .5rem; }
	.proof-body li { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: start; gap: .25rem .5rem; padding: .75rem; border: 1px solid var(--m3c-outline-variant); border-radius: .75rem; }
	.file-summary { display: grid; gap: .2rem; }
	.file-outcome { color: var(--m3c-on-surface-variant); }
	.file-message { grid-column: 1 / -1; font-size: .875rem; }
	.proof-body form { display: grid; gap: 1rem; min-width: 0; border-top: 1px solid var(--m3c-outline-variant); padding-top: 1rem; }
	.unattended { display: flex; align-items: flex-start; gap: .65rem; cursor: pointer; }
	.unattended input { flex-shrink: 0; width: 1.125rem; height: 1.125rem; margin: .2rem 0 0; }
	.photo-selection > :global(button), .confirm-action { justify-self: start; }
	.proof-body :global(button) { max-width: 100%; height: auto; min-height: 2.75rem; white-space: normal; padding-block: .65rem; line-height: 1.4; }
	.proof-body :global(:focus-visible), .status:focus { outline: 3px solid var(--m3c-primary, #1a5b9b); outline-offset: 3px; }
	.proof-body details { min-width: 0; font-size: .9rem; }
	.proof-body summary { cursor: pointer; padding-block: .5rem; }
	.proof-body details p { margin-block: .5rem; }
	.status { display: grid; gap: .25rem; padding: .75rem; border-radius: .5rem; background: var(--m3c-surface-container); font-size: .9rem; }
	@media (max-width: 36rem) {
		.proof-body { padding: 1rem; gap: 1rem; }
		.proof-body li { padding: .5rem; align-items: center; }
		.file-summary { display: contents; }
		.file-summary strong { grid-column: 1 / -1; }
		.file-outcome { grid-column: 1; grid-row: 2; }
		.remove-action { grid-column: 2; grid-row: 2; }
		.confirm-action, .confirm-action :global(button) { width: 100%; }
	}
</style>
