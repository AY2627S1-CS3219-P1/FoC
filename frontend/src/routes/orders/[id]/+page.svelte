<script lang="ts">
	import { page } from '$app/state';
	import { env } from '$env/dynamic/public';
	import { Button, Card, Dialog, Icon, LoadingIndicator, TextFieldOutlinedMultiline } from 'm3-svelte';
	import starFilled from '@ktibow/iconset-material-symbols/star-rate-rounded';
	import starOutline from '@ktibow/iconset-material-symbols/star-rate-outline-rounded';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { authService, orderApi, orderOffersApi, participantActionsApi, deliveryProofApi, reviewApi, snackbarService } from '$lib/services';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import { durationNanos, isTerminal, orderDeadline, statusLabel, timestampNanos } from '$lib/services/order/presentation';
	import { describeOrderError } from '$lib/services/order/errors';
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
	import OrderProgress from './OrderProgress.svelte';
	import PayoutChart from './PayoutChart.svelte';
	import ParticipantActions from '$lib/components/order-cancellation/ParticipantActions.svelte';
	import OrderOfferControls from '$lib/components/order-offers/OrderOfferControls.svelte';
	import DeliveryProofPanel from '$lib/components/order/DeliveryProofPanel.svelte';

	let order = $state<Order | null>(null);
	let error = $state('');
	let loading = $state(false);
	let now = $state(Date.now());
	let generation = 0;

	async function refresh() {
		const id = page.params.id!;
		const identity = authService.user?.id;
		if (!identity) return;
		const request = ++generation;
		loading = true;
		try {
			const value = await orderApi.getOrder(id);
			if (request === generation && page.params.id === id && authService.user?.id === identity) {
				order = value;
				error = '';
			}
		} catch (cause) {
			if (request === generation) error = describeOrderError(cause, 'refresh this order');
		} finally {
			if (request === generation) loading = false;
		}
	}

	$effect(() => {
		const id = page.params.id;
		const user = authService.user?.id;
		generation++;
		order = null;
		error = '';
		if (id && user) void refresh();
		return () => { generation++; };
	});

	$effect(() => {
		if (!authService.user) return;
		let lastPoll = Date.now();
		let checkedDeadline: number | undefined;
		const timer = setInterval(() => {
			now = Date.now();
			if (document.hidden || !order || isTerminal(order.status)) return;
			const deadline = orderDeadline(order);
			if (deadline && now >= deadline && checkedDeadline !== deadline) {
				checkedDeadline = deadline;
				lastPoll = now;
				void refresh();
			} else if (!loading && now - lastPoll >= 15000) {
				lastPoll = now;
				void refresh();
			}
		}, 1000);
		const visible = () => {
			if (!document.hidden) { lastPoll = Date.now(); void refresh(); }
		};
		document.addEventListener('visibilitychange', visible);
		window.addEventListener('online', visible);
		return () => { clearInterval(timer); document.removeEventListener('visibilitychange', visible); window.removeEventListener('online', visible); };
	});

	const deadline = $derived(order ? orderDeadline(order) : undefined);
	const seconds = $derived(deadline ? Math.max(0, Math.ceil((deadline - now) / 1000)) : undefined);

	// Roles and timing that decide what the page shows.
	const callerId = $derived(authService.user?.id ?? '');
	const isRequester = $derived(Boolean(order && order.requesterId === callerId));
	const isCourier = $derived(Boolean(order && order.courierId === callerId));
	// Payout inputs in integer nanoseconds, as Credit computes them.
	const allowedNanos = $derived(order?.deliveryDuration ? durationNanos(order.deliveryDuration) : 0n);
	const elapsedNanos = $derived.by(() => {
		if (!order?.pickedUpAt) return 0n;
		const end = order.deliveredAt ? timestampNanos(order.deliveredAt) : BigInt(now) * 1_000_000n;
		return end - timestampNanos(order.pickedUpAt);
	});
	const deliveryMinutes = $derived(Number(allowedNanos / 60_000_000_000n));
	// Wireframe: the payout graph only appears once delivery runs late.
	const late = $derived(
		Boolean(order?.pickedUpAt) && allowedNanos > 0n && elapsedNanos > allowedNanos &&
			(order?.status === OrderStatus.DELIVERING || Boolean(order?.deliveredAt))
	);
	const canChat = $derived(
		Boolean(order && (isRequester || isCourier) && order.courierId && !isTerminal(order.status))
	);
	const telegramBotUrl = env.PUBLIC_TELEGRAM_BOT_URL ?? '';

	// The caller's review for this request, rendered inline at the bottom of
	// the page. Fetching the full review (not just an existence flag) keeps the
	// rating, comment and freshly-signed photo URLs in one place.
	let myReview = $state<ReviewRecord | null>(null);
	let reviewLoading = $state(false);
	$effect(() => {
		if (!order || order.status !== OrderStatus.COMPLETED || !(isRequester || isCourier)) {
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

	// Only participants of a completed request can review, and only the other
	// participant is a valid reviewee.
	const reviewEligible = $derived(
		Boolean(order && order.status === OrderStatus.COMPLETED && (isRequester || isCourier))
	);
	const revieweeId = $derived(
		order ? (order.requesterId === callerId ? order.courierId : order.requesterId) : ''
	);
	const revieweeRole = $derived(
		order ? (order.requesterId === callerId ? 'courier' : 'requester') : ''
	);

	// Photos are staged by the shared controller; the review form owns one while
	// it is on screen and attaches the ordered ids to the submission.
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
				if (!validated.valid || !order) return;
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
					// Rejected rows are never retried; advertising "try again" would
					// send the user in a circle.
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
					// Swap the form for the stored review in place. The server copy
					// owns the photo grants; a fetch failure still shows what was sent.
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
						// A prior attempt likely committed despite an ambiguous
						// response; resubmitting cannot succeed.
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

	// Entering with #review (from the request history CTA) brings the review
	// section into view and puts the keyboard where the work starts.
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
	// MUI-style preview: hovering or focusing a star previews that score
	// without changing the selection until it is clicked or activated.
	let hoveredRating = $state<number | null>(null);
	const shownRating = $derived(hoveredRating ?? $reviewForm.rating ?? 0);

	// The chips carry the ARIA radiogroup pattern: arrow keys (and Home/End)
	// move focus and select, with a roving tabindex so Tab reaches the group
	// once instead of stopping at every chip.
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

	// Courier's "Update status" opens proof of delivery in a dialog.
	let proofOpen = $state(false);
	$effect(() => {
		if (order && order.status !== OrderStatus.DELIVERING) proofOpen = false;
	});

	const clock = new Intl.DateTimeFormat('en-SG', { hour: 'numeric', minute: '2-digit', timeZone: 'Asia/Singapore' });
	function countdown(total: number): string {
		const minutes = Math.floor(total / 60);
		return minutes >= 60 ? `${Math.floor(minutes / 60)} h ${minutes % 60} min` : `${minutes} min ${total % 60} s`;
	}
</script>

<svelte:head><title>{order?.itemDescription ?? 'Request'} · Friend on Campus</title></svelte:head>

{#snippet proofButtons()}
	<Button variant="text" onclick={() => (proofOpen = false)}>Close</Button>
{/snippet}

<section class="order-page" aria-labelledby="order-title">
	<div class="top">
		<Button variant="text" href={isRequester || isCourier ? '/orders/mine' : '/orders'}>{isRequester || isCourier ? 'My orders' : 'Open requests'}</Button>
		<Button variant="text" disabled={loading} onclick={() => void refresh()}>{loading ? 'Refreshing…' : 'Refresh'}</Button>
	</div>

	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to view this request.</p>
	{:else}
		{#if error}<p class="notice error" role="alert">{order ? 'This request may be out of date. ' : ''}{error}</p>{/if}
		{#if !order && loading}<div class="loading-state"><LoadingIndicator aria-label="Loading request" /></div>{/if}
		{#if order}
			<div class="layout">
				<div class="main">
					<Card variant="outlined">
						<div class="panel-body">
							<header class="title">
								<p class="status">{statusLabel(order.status)}</p>
								<h1 id="order-title">{order.itemDescription}</h1>
								{#if isCourier || isRequester}<p class="sub">{isCourier ? "You're the courier for this request." : 'You requested this.'}</p>{/if}
							</header>
							{#if order.closeReason && isTerminal(order.status)}<p class="notice">{order.closeReason}</p>{/if}
							<dl class="facts">
								<div><dt>Pick up</dt><dd>{order.pickup?.name ?? 'Unavailable'}<span class="sub">{order.pickup?.buildingName ?? ''}{order.pickup?.floor ? ', ' + order.pickup.floor : ''}</span></dd></div>
								<div><dt>Deliver to</dt><dd>
									{#if order.delivery?.location?.locationId}{order.delivery.location.name}<span class="sub">{order.delivery.location.buildingName}{order.delivery.location.floor ? ', ' + order.delivery.location.floor : ''}</span>
									{:else}{order.deliveryBuildingName || 'Area unavailable'}<span class="sub">The exact place is shared with the courier.</span>{/if}
								</dd></div>
								{#if order.delivery?.instructions}<div class="wide"><dt>Instructions</dt><dd class="prose">{order.delivery.instructions}</dd></div>{/if}
							</dl>
							{#if order.itemImages.length}
								<div class="images" aria-label="Item photos">
									{#each order.itemImages as image (image.id)}
										<a href={image.downloadUrl} target="_blank" rel="noreferrer"><img src={image.downloadUrl} alt="Item" loading="lazy" /></a>
									{/each}
								</div>
							{/if}
						</div>
					</Card>

					<Card variant="outlined">
						<div class="panel-body">
							<h2>Progress</h2>
							<OrderProgress {order} />
						</div>
					</Card>
				</div>

				<aside class="side" aria-label="Reward and actions">
					<Card variant="outlined">
						<div class="panel-body">
							{#if late}
								<PayoutChart reward={order.rewardCredits} {allowedNanos} {elapsedNanos} settled={order.status === OrderStatus.COMPLETED} />
							{:else}
								<div class="reward">
									<span class="amount">{order.rewardCredits.toString()}<span class="unit">credits</span></span>
									{#if order.status === OrderStatus.DELIVERING && seconds !== undefined}
										<span class="sub">Full reward if delivered by {clock.format(deadline!)}, in {countdown(seconds)}.</span>
									{:else if !order.pickedUpAt && deliveryMinutes}
										<span class="sub">Deliver within {deliveryMinutes} min of pickup for the full reward.</span>
									{/if}
									{#if order.status === OrderStatus.AWAITING_COURIER && seconds !== undefined}
										<span class="sub">{seconds > 0 ? `Open for couriers for ${countdown(seconds)}.` : 'Closing. Checking status…'}</span>
									{/if}
								</div>
							{/if}

							<div class="actions">
								{#if canChat && telegramBotUrl}
									<Button variant="tonal" href={telegramBotUrl} target="_blank" rel="noreferrer" title="Send /chat in the bot and choose this request">Chat on Telegram</Button>
								{/if}
								{#if isCourier && order.status === OrderStatus.DELIVERING}
									<Button onclick={() => (proofOpen = true)}>Update status: delivered</Button>
								{/if}
							</div>

							<ParticipantActions {order} {refresh} api={participantActionsApi} {callerId} />
						</div>
					</Card>

					{#if order.status === OrderStatus.AWAITING_COURIER}
						<Card variant="outlined">
							<div class="panel-body">
								<OrderOfferControls {order} {refresh} api={orderOffersApi} {callerId} suspended={authService.user?.role === UserRole.SUSPENDED_USER} />
							</div>
						</Card>
					{/if}

					{#if order.deliveredAt || (order.status !== OrderStatus.DELIVERING && order.pickedUpAt)}
						<Card variant="outlined">
							<div class="panel-body">
								{#key `${callerId}:${order.id}`}
									<DeliveryProofPanel {order} {refresh} api={deliveryProofApi} {callerId} />
								{/key}
							</div>
						</Card>
					{/if}
				</aside>
			</div>

			{#if reviewEligible}
				<Card variant="outlined">
					<div class="panel-body review-section" id="review-section">
						{#if reviewLoading && !myReview}
							<div class="loading-state"><LoadingIndicator aria-label="Loading your review" /></div>
						{:else if myReview}
							<header class="title">
								<h2 id="review-heading" tabindex="-1">Your review</h2>
								<p class="sub">
									{myReview.createdAt
										? `Reviewed ${reviewDate.format(new Date(myReview.createdAt))} SGT · `
										: ''}about the {revieweeRole}
								</p>
							</header>
							<div class="review-rating" role="img" aria-label={`Rated ${myReview.rating} out of 5`}>
								{#each reviewRatings as value (value)}
									<span class="star" class:filled={value <= myReview.rating} aria-hidden="true">
										<Icon icon={value <= myReview.rating ? starFilled : starOutline} size={26} />
									</span>
								{/each}
							</div>
							<p class="comment">{myReview.comment}</p>
							<ReviewPhotos photos={myReview.photos} />
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
												class:filled={value <= shownRating}
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
												<Icon
													icon={value <= shownRating ? starFilled : starOutline}
													size={34}
													aria-hidden="true"
												/>
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

			<Dialog bind:open={proofOpen} headline="Leave a photo as proof of delivery" buttons={proofButtons}>
				{#key `${callerId}:${order.id}:${proofOpen}`}
					<DeliveryProofPanel {order} {refresh} api={deliveryProofApi} {callerId} />
				{/key}
			</Dialog>
		{/if}
	{/if}
</section>

<style>
	.order-page { display: grid; gap: 1rem; max-width: 76rem; margin: 0 auto; }
	.top { display: flex; justify-content: space-between; gap: 0.5rem; }
	.layout { display: grid; grid-template-columns: minmax(0, 3fr) minmax(18rem, 2fr); gap: 1.5rem; align-items: start; }
	.main, .side { display: grid; gap: 1rem; min-width: 0; }
	.side { position: sticky; top: 1rem; }
	.panel-body.review-section { scroll-margin-top: 1rem; gap: 0.4rem; padding: 1rem; }
	.review-form { display: grid; gap: 1rem; min-width: 0; }
	.rating { display: grid; gap: 0.25rem; margin: 0; padding: 0; border: 0; }
	.rating legend { padding: 0; margin-bottom: 0.5rem; font-weight: 600; }
	.rating-options { display: flex; flex-wrap: wrap; align-items: center; gap: 0.15rem; }
	.star-chip { display: inline-grid; place-items: center; padding: 0.15rem; border: 0; border-radius: 999px; background: transparent; color: var(--m3c-outline); line-height: 0; cursor: pointer; }
	.star-chip.filled { color: #faaf00; }
	.star-chip:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	.field { display: grid; gap: 0.25rem; min-width: 0; }
	.field-error { color: var(--m3c-error, #b3261e); }
	.form-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
	.review-rating { display: flex; align-items: center; gap: 0.3rem; }
	.star { display: inline-grid; place-items: center; color: var(--m3c-outline); line-height: 0; }
	.star.filled { color: #faaf00; }
	.comment { margin: 0; white-space: pre-wrap; }
	#review-heading:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: 3px; }

	.title { display: grid; gap: 0.3rem; }
	.status { margin: 0; font-weight: 650; color: var(--m3c-primary); }
	h1 { margin: 0; font-size: clamp(1.5rem, 3vw, 2rem); line-height: 1.2; overflow-wrap: anywhere; }
	h2 { margin: 0; font-size: 1.1rem; }

	.facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; margin: 0; }
	.facts .wide { grid-column: 1 / -1; }
	dt { font-size: 0.85rem; font-weight: 600; color: var(--m3c-on-surface-variant); }
	dd { display: grid; margin: 0.15rem 0 0; font-weight: 600; overflow-wrap: anywhere; }
	.prose { font-weight: 400; white-space: pre-wrap; }
	.sub { font-size: 0.875rem; font-weight: 400; line-height: 1.4; color: var(--m3c-on-surface-variant); }

	.images { display: flex; flex-wrap: wrap; gap: 0.5rem; }
	img { width: 6rem; height: 6rem; object-fit: cover; border-radius: var(--m3-shape-small, 8px); }

	.reward { display: grid; gap: 0.35rem; }
	.amount { font-size: 2.25rem; font-weight: 750; line-height: 1; font-variant-numeric: tabular-nums; }
	.unit { margin-inline-start: 0.35rem; font-size: 1rem; font-weight: 600; }

	.actions { display: grid; justify-items: start; gap: 0.5rem; }

	@media (max-width: 60rem) {
		.layout { grid-template-columns: minmax(0, 1fr); }
		.side { position: static; grid-row: 1; }
	}
	@media (max-width: 36rem) {
		.facts { grid-template-columns: minmax(0, 1fr); }
	}
</style>
