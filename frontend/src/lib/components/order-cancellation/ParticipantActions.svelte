<script lang="ts">
	import { onDestroy } from 'svelte';
	import { Button, Dialog, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { CancellationStatus, OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import { cancellationSchema, dispositionSchema } from '$lib/services/order-cancellation/forms';
	import { cancellationStorageKey, loadCancellation, type CancellationSubmission, type ParticipantActionsApi } from '$lib/services/order-cancellation';

	let { order, callerId, api, refresh }: {
		order: Order; callerId: string; api: ParticipantActionsApi; refresh: () => Promise<void>;
	} = $props();
	let error = $state(''), message = $state('');
	let saved = $state<CancellationSubmission | null>(null), busy = $state(false), ready = $state(false);
	let confirmOpen = $state(false);
	let confirmation = $state<{ input: CancellationSubmission; consequence: string; epoch: number } | null>(null);
	let scope = '', epoch = 0;
	const requester = $derived(order.requesterId === callerId);
	const courier = $derived(order.courierId === callerId);
	const canCancel = $derived((requester && [OrderStatus.AWAITING_COURIER, OrderStatus.AWAITING_PICKUP, OrderStatus.DELIVERING].includes(order.status)) ||
		(courier && [OrderStatus.AWAITING_PICKUP, OrderStatus.DELIVERING].includes(order.status)));
	const canRecord = $derived(courier && Boolean(order.pickedUpAt) && [OrderStatus.CANCELLED, OrderStatus.FAILED].includes(order.status));
	const consequence = $derived(courier && order.status === OrderStatus.AWAITING_PICKUP
		? 'Withdraw before pickup. Once the reward is ready, another courier can accept the order.'
		: courier ? 'Cancel after pickup. The order fails and reserved credits return to the requester. An account warning will be requested.'
		: order.pickedUpAt ? 'Cancel after pickup. The reserved reward is paid to the courier.'
		: 'Cancel before pickup. Reserved credits return to you.');

	const { form: cancelForm, errors: cancelErrors, enhance: enhanceCancel, reset: resetCancel } = superForm(defaults(zod4(cancellationSchema)), {
		SPA: true, validators: zod4Client(cancellationSchema), resetForm: false, multipleSubmits: 'prevent',
		onUpdate({ form }) {
			if (!form.valid || busy || !ready || !canCancel || saved) return;
			confirmation = {
				input: { orderId: order.id, expectedRevision: order.revision.toString(), reason: form.data.reason, key: crypto.randomUUID() },
				consequence, epoch
			};
			confirmOpen = true;
		}
	});
	const { form: locationForm, errors: locationErrors, enhance: enhanceLocation, reset: resetLocation } = superForm(defaults(zod4(dispositionSchema)), {
		SPA: true, validators: zod4Client(dispositionSchema), resetForm: false, multipleSubmits: 'prevent',
		async onUpdate({ form }) { if (form.valid) await perform('location', form.data.location); }
	});

	$effect(() => {
		const next = `${callerId}:${order.id}`;
		if (next === scope) return;
		scope = next; epoch++; busy = false; ready = false; error = ''; message = ''; saved = null;
		confirmOpen = false; confirmation = null;
		resetCancel({ data: { reason: '' } }); resetLocation({ data: { location: '' } });
		if (!callerId) return;
		try {
			saved = loadCancellation(sessionStorage, callerId, order.id);
			resetCancel({ data: { reason: saved?.reason ?? '' } });
			ready = true;
			if (saved) message = 'Your cancellation is still being confirmed. Try again to check the result.';
		} catch { error = 'Could not restore your cancellation. Check My Orders before trying again.'; }
	});
	onDestroy(() => { epoch++; });

	async function confirmCancellation() {
		const selected = confirmation;
		confirmOpen = false; confirmation = null;
		if (selected?.epoch === epoch) await cancel(selected.input);
	}

	async function cancel(input: CancellationSubmission) {
		if (busy || !ready || !callerId || input.orderId !== order.id) return;
		const current = epoch, caller = callerId, id = order.id;
		let sent = false;
		busy = true; error = ''; message = 'Submitting cancellation…';
		try {
			sessionStorage.setItem(cancellationStorageKey(caller, id), JSON.stringify(input));
			saved = input; sent = true;
			const result = await api.cancel(input);
			if (current !== epoch) return;
			if (result.status === CancellationStatus.PENDING) {
				message = 'Still confirming your withdrawal. Pickup is paused. Try again to check the result.';
			} else {
				message = result.status === CancellationStatus.SUPERSEDED
					? 'The order changed before your withdrawal finished. Check its current status.'
					: 'Cancellation confirmed. Your credit balance may take a moment to update.';
				try { sessionStorage.removeItem(cancellationStorageKey(caller, id)); saved = null; }
				catch { ready = false; error = 'Confirmed, but we could not finish updating this page. Check My Orders before trying again.'; }
			}
		} catch (cause) {
			if (current !== epoch) return;
			const code = ConnectError.from(cause).code;
			if (!sent) {
				ready = false; message = 'Cancellation not submitted.';
				error = 'Cancellation was not sent. Check that your browser allows site storage, then try again.';
			} else if ([Code.Aborted, Code.InvalidArgument, Code.FailedPrecondition].includes(code)) {
				try { sessionStorage.removeItem(cancellationStorageKey(caller, id)); saved = null; }
				catch { ready = false; }
				error = 'The order changed or could not be cancelled. Refresh, check the details, and try again.';
				message = 'Cancellation rejected.';
			} else {
				error = 'We could not confirm your cancellation. Try again to check the result.';
				message = 'Cancellation not yet confirmed.';
			}
		} finally {
			if (current === epoch) {
				try { await refresh(); } catch { if (current === epoch) error += ' Could not update the order. Refresh to try again.'; }
				if (current === epoch) busy = false;
			}
		}
	}

	async function perform(action: 'pickup' | 'receipt' | 'location', location = '') {
		if (busy || saved || !callerId) return;
		const current = epoch, id = order.id;
		busy = true; error = ''; message = `${action === 'location' ? 'Final location' : action} pending.`;
		try {
			if (action === 'pickup') await api.pickup(id);
			else if (action === 'receipt') await api.receipt(id);
			else await api.recordLocation(id, location);
			if (current !== epoch) return;
			message = action === 'location' ? 'Final item location saved.' : action === 'pickup' ? 'Pickup confirmed.' : 'Receipt confirmed. Your credit balance may take a moment to update.';
		} catch {
			if (current !== epoch) return;
			error = `${action === 'location' ? 'Saving final location' : action === 'pickup' ? 'Confirming pickup' : 'Confirming receipt'} is unconfirmed. Your input is still here. Refresh and retry if the action is still available.`;
		} finally {
			if (current === epoch) {
				try { await refresh(); } catch { if (current === epoch) error += ' Refresh failed.'; }
				if (current === epoch) busy = false;
			}
		}
	}
</script>

<section aria-label="Participant actions" class="participant-actions">
	{#if error}<p role="alert">{error}</p>{/if}
	{#if message}<p role="status">{message}</p>{/if}
	{#if saved}
		<p>Cancellation reason: {saved.reason || 'Not supplied'}</p>
		<Button variant="outlined" disabled={busy || !ready} onclick={() => { if (saved) void cancel(saved); }}>Check cancellation</Button>
	{:else if canCancel}
		<p>{consequence}</p>
		<form method="POST" use:enhanceCancel>
			<div class="field">
			<TextFieldOutlinedMultiline label="Cancellation reason (optional)" name="reason" bind:value={$cancelForm.reason} disabled={busy} rows={3} error={Boolean($cancelErrors.reason)} aria-invalid={$cancelErrors.reason ? 'true' : undefined} aria-describedby={$cancelErrors.reason ? 'cancellation-reason-help cancellation-reason-error' : 'cancellation-reason-help'} />
			<small id="cancellation-reason-help" class="secondary-text">Up to 500 characters.</small>
			{#if $cancelErrors.reason}<p id="cancellation-reason-error" class="field-error" role="alert">{$cancelErrors.reason[0]}</p>{/if}
			</div>
			<Button type="submit" variant="outlined" disabled={busy || !ready || (courier && order.cancellationPending)}>{courier && order.status === OrderStatus.AWAITING_PICKUP ? 'Withdraw' : 'Cancel order'}</Button>
		</form>
	{/if}
	{#if order.cancellationPending}<p role="status">Still confirming the withdrawal. Pickup is paused.</p>{/if}
	{#if courier && order.status === OrderStatus.AWAITING_PICKUP}
		<Button disabled={busy || Boolean(saved) || order.cancellationPending} onclick={() => void perform('pickup')}>Confirm pickup</Button>
	{/if}
	{#if requester && order.status === OrderStatus.DELIVERED}
		<Button disabled={busy || Boolean(saved)} onclick={() => void perform('receipt')}>Confirm receipt</Button>
	{/if}
	{#if (requester || courier) && order.pickedUpAt && [OrderStatus.CANCELLED, OrderStatus.FAILED].includes(order.status)}
		<p>Final item location: {order.itemLocation || 'Final location pending'}</p>
	{:else if (requester || courier) && order.status === OrderStatus.CANCELLED && !order.pickedUpAt}
		<p>Cancelled before pickup. The courier does not hold the item.</p>
	{/if}
	{#if canRecord}
		<form method="POST" use:enhanceLocation>
			<div class="field">
			<TextFieldOutlinedMultiline label="Final item location" name="location" bind:value={$locationForm.location} disabled={busy} rows={3} error={Boolean($locationErrors.location)} aria-invalid={$locationErrors.location ? 'true' : undefined} aria-describedby={$locationErrors.location ? 'final-location-help final-location-error' : 'final-location-help'} />
			<small id="final-location-help" class="secondary-text">1 to 500 characters.</small>
			{#if $locationErrors.location}<p id="final-location-error" class="field-error" role="alert">{$locationErrors.location[0]}</p>{/if}
			</div>
			<Button type="submit" disabled={busy || Boolean(saved)}>Save final item location</Button>
		</form>
	{/if}
</section>

{#snippet confirmationButtons()}
	<Button variant="text" onclick={() => { confirmOpen = false; confirmation = null; }}>Keep order</Button>
	<Button disabled={busy} onclick={() => void confirmCancellation()}>Confirm cancellation</Button>
{/snippet}
<Dialog bind:open={confirmOpen} headline="Confirm cancellation" aria-label="Confirm cancellation" buttons={confirmationButtons}>
	{#if confirmation}
		<p>{confirmation.consequence}</p>
		<p>Your credit balance may take a moment to update.</p>
	{/if}
</Dialog>

<style>
	.participant-actions, form { display: grid; gap: .75rem; min-width: 0; }
	.participant-actions { margin-top: 1rem; }
	form { width: 100%; }
	.field :global(.m3-container) { width: 100%; min-width: 0; }
	.participant-actions :global(button) { justify-self: start; max-width: 100%; height: auto; min-height: 2.5rem; padding-block: .5rem; white-space: normal; }
	p { margin: 0; overflow-wrap: anywhere; }
</style>
