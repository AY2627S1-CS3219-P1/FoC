<script lang="ts">
	import { Button, Dialog, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import {
		ReviewPhotosController,
		type ReviewPhotosSnapshot
	} from '$lib/features/review-photos/controller';
	import type { ReviewApi } from '$lib/services/review/reviewApi';
	import { reviewSchema } from '$lib/services/review/reviewSchema';
	import { participationLabel, statusLabel } from '$lib/services/order/presentation';
	import ReviewPhotoPicker from './ReviewPhotoPicker.svelte';

	// The Add Review dialog from the wireframe: order details, rating, a short
	// description and the photo upload strip, submitted with ordered photo ids.
	let { order, callerId, api, onclose, onsubmitted }: {
		order: Order;
		callerId: string;
		api: ReviewApi;
		onclose: () => void;
		onsubmitted: () => void;
	} = $props();
	let open = $state(true);

	// The caller reviews the other participant of the completed request.
	const revieweeId = $derived(order.requesterId === callerId ? order.courierId : order.requesterId);

	let photoView = $state<ReviewPhotosSnapshot>({ files: [], outcome: 'Not Submitted', message: '', busy: false });
	let photoController: ReviewPhotosController | undefined = $state();
	$effect(() => {
		const instance = new ReviewPhotosController(api, (snapshot) => { photoView = snapshot; });
		photoController = instance;
		photoView = instance.snapshot();
		return () => instance.dispose();
	});

	const { form, errors, message, submitting, enhance } = superForm(defaults(zod4(reviewSchema)), {
		SPA: true,
		validators: zod4Client(reviewSchema),
		multipleSubmits: 'prevent',
		resetForm: false,
		async onUpdate({ form: validated }) {
			if (!validated.valid) return;
			if (!revieweeId || revieweeId === callerId) {
				setMessage(validated, 'This request has no counterpart to review.');
				return;
			}
			if (photoView.busy || photoView.files.some((row) => row.outcome === 'Pending')) {
				setMessage(validated, 'Wait for the photos to finish uploading, then submit again.');
				return;
			}
			const failed = photoView.files.filter((row) => row.outcome === 'Failed' || row.outcome === 'Rejected');
			if (failed.length > 0) {
				setMessage(validated, 'Some photos did not upload. Remove them to submit without, or try uploading again.');
				return;
			}
			try {
				await api.submit({
					from: callerId,
					to: revieweeId,
					orderId: order.id,
					rating: validated.data.rating,
					comment: validated.data.comment,
					photoIds: photoController?.photoIds() ?? []
				}, AbortSignal.timeout(15000));
				onsubmitted();
				open = false;
			} catch {
				setMessage(validated, 'We could not submit your review. Your photos are kept; please try again.');
			}
		}
	});

	// Ratings are chosen with five chips; 1 is the lowest, 5 the highest.
	const ratings = [1, 2, 3, 4, 5];
</script>

<Dialog bind:open headline="Leave a review" onclose={onclose}>
	{#snippet buttons()}
		<Button type="button" onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="leave-review-form" disabled={$submitting || photoView.busy}>
			{$submitting ? 'Submitting…' : 'Submit review'}
		</Button>
	{/snippet}
	<div class="review-dialog-body">
		<section class="order-details" aria-label="Order details">
			<h2>{order.itemDescription}</h2>
			<p>
				Reviewing the {participationLabel(order, callerId) === 'Requested' ? 'courier' : 'requester'} ·
				{statusLabel(order.status).toLowerCase()}
			</p>
		</section>
		<form id="leave-review-form" class="review-form" method="POST" use:enhance aria-label="Leave a review">
			<fieldset class="rating">
				<legend>Rating</legend>
				<div class="rating-options" role="radiogroup" aria-label="Rating from 1 to 5 stars">
					{#each ratings as value (value)}
						<button
							type="button"
							class="rating-chip"
							class:chosen={$form.rating === value}
							role="radio"
							aria-checked={$form.rating === value}
							aria-label={`${value} star${value > 1 ? 's' : ''}`}
							onclick={() => ($form.rating = value)}
						>
							{value}
						</button>
					{/each}
				</div>
				{#if $errors.rating}<small class="field-error">{$errors.rating[0]}</small>{/if}
			</fieldset>
			<div class="field">
				<TextFieldOutlinedMultiline
					label="Description"
					name="comment"
					rows={4}
					required
					bind:value={$form.comment}
					error={!!$errors.comment}
					aria-invalid={$errors.comment ? 'true' : undefined}
					aria-describedby={$errors.comment ? 'review-comment-error' : undefined}
				/>{#if $errors.comment}<small class="field-error" id="review-comment-error"
						>{$errors.comment[0]}</small
					>{/if}
			</div>
			<ReviewPhotoPicker controller={photoController} view={photoView} disabled={$submitting} />
			{#if $message}<p class="notice" role="status" aria-live="polite">{$message}</p>{/if}
		</form>
	</div>
</Dialog>

<style>
	.review-dialog-body { display: grid; gap: 1rem; min-width: 0; line-height: 1.5; }
	.order-details { display: grid; gap: 0.1rem; }
	.order-details h2 { margin: 0; font-size: 1.05rem; overflow-wrap: anywhere; }
	.order-details p { margin: 0; color: var(--m3c-on-surface-variant); font-size: 0.9rem; }
	.review-form { display: grid; gap: 1rem; min-width: 0; }
	.rating { display: grid; gap: 0.4rem; margin: 0; padding: 0; border: 0; }
	.rating legend { padding: 0; font-weight: 600; }
	.rating-options { display: flex; flex-wrap: wrap; gap: 0.4rem; }
	.rating-chip { display: inline-grid; place-items: center; min-width: 2.75rem; height: 2.75rem; padding: 0 0.6rem; border: 1px solid var(--m3c-outline); border-radius: 2rem; background: transparent; color: var(--m3c-on-surface); font-weight: 600; cursor: pointer; }
	.rating-chip.chosen { background: var(--m3c-primary); border-color: var(--m3c-primary); color: var(--m3c-on-primary, #fff); }
	.rating-chip:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 3px; }
	.field { display: grid; gap: 0.25rem; min-width: 0; }
	.field-error { color: var(--m3c-error, #b3261e); }
	.notice { margin: 0; padding: 0.75rem; border-radius: 0.5rem; background: var(--m3c-surface-container); font-size: 0.9rem; }
</style>
