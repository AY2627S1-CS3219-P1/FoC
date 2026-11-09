<script lang="ts">
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { reviewApi, snackbarService } from '$lib/services';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import {
		ReviewPhotosController,
		type ReviewPhotosSnapshot
	} from '$lib/features/review-photos/controller';
	import {
		ReviewAlreadyExistsError,
		ReviewLimitedError,
		ReviewNotReadyError,
		ReviewRejectedError,
		type ReviewRecord
	} from '$lib/services/review/reviewApi';
	import { reviewSchema } from '$lib/services/review/reviewSchema';
	import PhotoUploader from '$lib/components/photo-uploader/PhotoUploader.svelte';
	import ReviewPhotos from '$lib/components/review/ReviewPhotos.svelte';

	// Review card at the bottom of a request page: the caller's stored review or
	// the form that creates it.
	let { order, callerId }: { order: Order; callerId: string } = $props();

	const isRequester = $derived(order.requesterId === callerId);
	const isCourier = $derived(order.courierId === callerId);

	// Only participants of a completed request can review, and only the other
	// participant is a valid reviewee.
	const reviewEligible = $derived(
		order.status === OrderStatus.COMPLETED && (isRequester || isCourier)
	);
	const revieweeId = $derived(isRequester ? order.courierId : order.requesterId);
	const revieweeRole = $derived(isRequester ? 'courier' : 'requester');

	let myReview = $state<ReviewRecord | null>(null);
	let reviewLoading = $state(false);
	$effect(() => {
		if (!reviewEligible) {
			myReview = null;
			return;
		}
		let active = true;
		const id = order.id;
		reviewLoading = true;
		reviewApi
			.getMyReviewForOrder(id, AbortSignal.timeout(15000))
			.then((review) => {
				if (active) myReview = review;
			})
			.catch(() => {
				if (active) myReview = null;
			})
			.finally(() => {
				if (active) reviewLoading = false;
			});
		return () => (active = false);
	});
	const reviewDate = new Intl.DateTimeFormat('en-SG', {
		dateStyle: 'medium',
		timeStyle: 'short',
		timeZone: 'Asia/Singapore'
	});

	let photoView = $state<ReviewPhotosSnapshot>({ files: [], outcome: 'Not Submitted', message: '', busy: false });
	let photoController: ReviewPhotosController | undefined = $state();
	$effect(() => {
		const instance = new ReviewPhotosController(reviewApi, (snapshot) => {
			photoView = snapshot;
		});
		photoController = instance;
		photoView = instance.snapshot();
		return () => instance.dispose();
	});

	// Only actionable photo guidance reaches the snackbar; progress and success
	// stay on the rows.
	let lastPhotoMessage = '';
	$effect(() => {
		if (photoView.message && photoView.message !== lastPhotoMessage) {
			lastPhotoMessage = photoView.message;
			snackbarService.show(photoView.message, { severity: 'info', timeout: 6000 });
		} else if (!photoView.message) {
			lastPhotoMessage = '';
		}
	});

	const reviewDefaults = defaults(zod4(reviewSchema));
	const { form: reviewForm, errors: reviewErrors, submitting: reviewSubmitting, enhance: reviewEnhance } =
		superForm(reviewDefaults, {
			SPA: true,
			validators: zod4Client(reviewSchema),
			multipleSubmits: 'prevent',
			resetForm: false,
			async onUpdate({ form: validated }) {
				if (!validated.valid) return;
				if (!revieweeId || revieweeId === callerId) {
					snackbarService.show('This request has no counterpart to review.', { severity: 'warning' });
					return;
				}
				if (photoView.busy || photoView.files.some((row) => row.outcome === 'Pending')) {
					snackbarService.show('Wait for the photos to finish uploading, then submit again.', { severity: 'warning' });
					return;
				}
				if (photoView.files.some((row) => row.outcome === 'Not Submitted')) {
					snackbarService.show('Upload your photos or remove them before submitting.', { severity: 'warning' });
					return;
				}
				if (photoView.files.some((row) => row.outcome === 'Failed')) {
					snackbarService.show('Some photos did not upload. Remove them to submit without, or retry.', { severity: 'warning' });
					return;
				}
				if (photoView.files.some((row) => row.outcome === 'Rejected')) {
					snackbarService.show("Some photos can't be used. Remove them to submit without photos.", { severity: 'warning' });
					return;
				}
				const target = order;
				try {
					await reviewApi.submit({
						from: callerId,
						to: revieweeId,
						orderId: target.id,
						rating: validated.data.rating,
						comment: validated.data.comment,
						photoIds: photoController?.photoIds() ?? []
					}, AbortSignal.timeout(15000));
					snackbarService.show('Review submitted.', { severity: 'success' });
					// Swap the form for the stored review. The server copy owns the
					// photo grants; a fetch failure still shows what was sent.
					try {
						myReview = await reviewApi.getMyReviewForOrder(target.id, AbortSignal.timeout(15000));
					} catch {
						myReview = null;
					}
					myReview ??= {
						id: '',
						reviewerId: callerId,
						revieweeId,
						reviewerName: '',
						revieweeName: '',
						orderId: target.id,
						rating: validated.data.rating,
						comment: validated.data.comment,
						createdAt: Date.now(),
						photos: []
					};
				} catch (cause) {
					if (cause instanceof ReviewAlreadyExistsError) {
						snackbarService.show(cause.message, { severity: 'info', timeout: 8000 });
						try {
							myReview = await reviewApi.getMyReviewForOrder(target.id, AbortSignal.timeout(15000));
						} catch {
							myReview = null;
						}
					} else if (cause instanceof ReviewLimitedError) {
						snackbarService.show(cause.message, { severity: 'warning', timeout: 8000 });
					} else if (cause instanceof ReviewNotReadyError) {
						photoController?.markNotReady(
							cause.photoIds,
							"This photo didn't make it. Remove and re-add it, or submit without it."
						);
						snackbarService.show('Some photos did not make it. Re-add them, or submit without them.', { severity: 'warning', timeout: 8000 });
					} else if (cause instanceof ReviewRejectedError && cause.reason === 'UPLOAD_UNAVAILABLE') {
						snackbarService.show(cause.message, { severity: 'error', timeout: 8000 });
					} else {
						snackbarService.show('We could not submit your review. Your photos are kept; please try again.', { severity: 'error', timeout: 8000 });
					}
				}
			}
		});

	// Entering with #review (from the request history CTA) brings the card into
	// view and puts the keyboard where the work starts.
	$effect(() => {
		if (typeof document === 'undefined') return;
		if (page.url.hash !== '#review' || !reviewEligible || reviewLoading) return;
		const section = document.getElementById('review-section');
		if (!section) return;
		section.scrollIntoView({ behavior: 'smooth', block: 'start' });
		if (myReview) {
			document.getElementById('review-heading')?.focus();
		} else {
			section.querySelector<HTMLElement>('.star-chip[tabindex="0"]')?.focus();
		}
	});

	const reviewRatings = [1, 2, 3, 4, 5];
	let ratingChips: HTMLButtonElement[] = [];
	// Hovering or focusing a star previews that score without committing it.
	let hoveredRating = $state<number | null>(null);
	const shownRating = $derived(hoveredRating ?? $reviewForm.rating ?? 0);

	// Arrow keys and Home/End follow the radiogroup pattern with a roving
	// tabindex, so Tab reaches the group once.
	function onRatingKeydown(event: KeyboardEvent) {
		if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) {
			return;
		}
		event.preventDefault();
		const current = Number((event.target as HTMLElement).getAttribute('data-rating') ?? 1);
		let next: number;
		if (event.key === 'Home') {
			next = 1;
		} else if (event.key === 'End') {
			next = reviewRatings.length;
		} else if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') {
			next = current === 1 ? reviewRatings.length : current - 1;
		} else {
			next = current === reviewRatings.length ? 1 : current + 1;
		}
		$reviewForm.rating = next;
		ratingChips[next - 1]?.focus();
	}
</script>

{#if reviewEligible}
	<Card variant="outlined">
		<div class="panel-body review-card" id="review-section">
			{#if reviewLoading && !myReview}
				<div class="loading-state"><LoadingIndicator aria-label="Loading your review" /></div>
			{:else if myReview}
				<header class="title">
					<h2 id="review-heading" tabindex="-1">Your review</h2>
					<p class="sub">
						{myReview.createdAt
							? `${reviewDate.format(new Date(myReview.createdAt))} · `
							: ''}about the {revieweeRole}
					</p>
				</header>
				<p class="review-rating" role="img" aria-label={`Rated ${myReview.rating} out of 5`}>
					<span class="stars" aria-hidden="true">{'★'.repeat(myReview.rating)}{'☆'.repeat(5 - myReview.rating)}</span>
				</p>
				<p class="comment">{myReview.comment}</p>
				<div class="review-photos"><ReviewPhotos photos={myReview.photos} /></div>
			{:else}
				<header class="title">
					<h2 id="review-heading" tabindex="-1">Leave a review</h2>
					<p class="sub">Tell others how this request went with the {revieweeRole}.</p>
				</header>
				<form id="review-form" class="review-form" method="POST" use:reviewEnhance aria-label="Leave a review">
					<fieldset class="rating">
						<legend>Rating</legend>
						<div
							class="rating-options"
							role="radiogroup"
							tabindex="-1"
							aria-label="Rating from 1 to 5 stars"
							onmouseleave={() => (hoveredRating = null)}
						>
							{#each reviewRatings as value, index (value)}
								<button
									type="button"
									class="star-chip"
									role="radio"
									aria-checked={$reviewForm.rating === value}
									aria-label={`${value} star${value > 1 ? 's' : ''}`}
									tabindex={($reviewForm.rating ?? 1) === value ? 0 : -1}
									data-rating={value}
									bind:this={ratingChips[index]}
									onclick={() => ($reviewForm.rating = value)}
									onmouseenter={() => (hoveredRating = value)}
									onfocus={() => (hoveredRating = value)}
									onblur={() => (hoveredRating = null)}
									onkeydown={onRatingKeydown}
								>
									<span class="glyph" aria-hidden="true">{value <= shownRating ? '★' : '☆'}</span>
								</button>
							{/each}
						</div>
						{#if $reviewErrors.rating}<small class="field-error">{$reviewErrors.rating[0]}</small>{/if}
					</fieldset>
					<div class="field">
						<TextFieldOutlinedMultiline
							label="Description"
							name="comment"
							rows={4}
							required
							bind:value={$reviewForm.comment}
							error={!!$reviewErrors.comment}
							aria-invalid={$reviewErrors.comment ? 'true' : undefined}
							aria-describedby={$reviewErrors.comment ? 'review-comment-error' : undefined}
						/>{#if $reviewErrors.comment}<small class="field-error" id="review-comment-error"
								>{$reviewErrors.comment[0]}</small
							>{/if}
					</div>
					<PhotoUploader controller={photoController} view={photoView} disabled={$reviewSubmitting} />
					<div class="form-actions">
						<Button type="submit">{$reviewSubmitting ? 'Submitting…' : 'Submit review'}</Button>
					</div>
				</form>
			{/if}
		</div>
	</Card>
{/if}

<style>
	.review-card { scroll-margin-top: 1rem; gap: 0.2rem; }
	.review-form { display: grid; gap: 1rem; min-width: 0; margin-top: 0.5rem; }
	.rating { display: grid; gap: 0.25rem; margin: 0; padding: 0; border: 0; }
	.rating legend { padding: 0; margin-bottom: 0.5rem; font-weight: 600; }
	.rating-options { display: flex; flex-wrap: wrap; align-items: center; gap: 0.15rem; }
	.star-chip { display: inline-grid; place-items: center; min-width: 2rem; padding: 0.1rem; border: 0; border-radius: 999px; background: transparent; color: var(--m3c-primary); font-size: 1.75rem; line-height: 1; cursor: pointer; }
	.star-chip:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	.field { display: grid; gap: 0.25rem; min-width: 0; }
	.field-error { color: var(--m3c-error, #b3261e); }
	.form-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
	.review-card .sub { margin: 0; }
	.review-rating { margin: 0; line-height: 1; }
	.review-photos { margin-top: 0.4rem; }
	.stars { color: var(--m3c-primary); letter-spacing: 0.1rem; }
	.comment { margin: 0; white-space: pre-wrap; }
	#review-heading:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: 3px; }
	.title { display: grid; gap: 0.15rem; }
	h2 { margin: 0; font-size: 1.1rem; }
	.sub { font-size: 0.875rem; font-weight: 400; line-height: 1.4; color: var(--m3c-on-surface-variant); }
	.loading-state { display: grid; place-items: center; padding: 2rem; }
</style>
