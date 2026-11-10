<script lang="ts">
	import { LoadingIndicator } from 'm3-svelte';
	import { orderApi, reviewApi } from '$lib/services';
	import { dateLabel, statusLabel } from '$lib/services/order/presentation';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import type { ReviewRecord } from '$lib/services/review/reviewApi';
	import type { ReportTarget } from '$lib/services/report/reportApi';
	import ReviewPhotos from '$lib/components/review/ReviewPhotos.svelte';

	// What a report is about: the order, or the review. Order Service and User
	// Service decide who may read it, so a failure here never blocks the report.
	let { target }: { target: ReportTarget } = $props();

	let order = $state<Order | null>(null);
	let review = $state<{ review: ReviewRecord; removed: boolean } | null>(null);
	let loading = $state(true);
	let failed = $state(false);

	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeZone: 'Asia/Singapore' });

	$effect(() => {
		const current = target;
		const controller = new AbortController();
		loading = true;
		failed = false;
		order = null;
		review = null;

		const load =
			current.kind === 'order'
				? orderApi.getOrder(current.orderId).then((value) => {
						order = value;
					})
				: reviewApi.getReview(current.reviewId, controller.signal).then((value) => {
						review = value;
					});
		load
			.catch(() => {
				if (!controller.signal.aborted) failed = true;
			})
			.finally(() => {
				if (!controller.signal.aborted) loading = false;
			});
		return () => controller.abort();
	});
</script>

<section class="target" aria-labelledby="report-target-title">
	<h2 id="report-target-title">{target.kind === 'order' ? 'Reported order' : 'Reported review'}</h2>
	{#if loading}
		<div class="loading" role="status"><LoadingIndicator aria-label="Loading" /></div>
	{:else if failed}
		<p class="secondary-text" role="status">
			The {target.kind} could not be loaded. It may have been removed, or you may not have access to it.
		</p>
	{:else if order}
		<div class="card">
			<div class="card-head">
				<strong>{order.itemDescription || 'Order'}</strong>
				<span class="badge">{statusLabel(order.status)}</span>
			</div>
			<dl>
				<div><dt>Pick up</dt><dd>{order.pickup?.name || 'Unknown'}{order.pickup?.buildingName ? `, ${order.pickup.buildingName}` : ''}</dd></div>
				<div><dt>Deliver to</dt><dd>{order.deliveryBuildingName || 'Unknown'}</dd></div>
				<div><dt>Reward</dt><dd>{order.rewardCredits.toString()} credits</dd></div>
				<div><dt>Created</dt><dd>{dateLabel(order.createdAt)}</dd></div>
				{#if order.closeReason}<div><dt>Closed because</dt><dd>{order.closeReason}</dd></div>{/if}
			</dl>
			<a href={`/orders/${encodeURIComponent(order.id)}`}>Open order</a>
		</div>
	{:else if review}
		<div class="card">
			<div class="card-head">
				<strong>{review.review.reviewerName || 'A user'} reviewed {review.review.revieweeName || 'a user'}</strong>
				{#if review.removed}<span class="badge removed">Removed</span>{/if}
			</div>
			<p class="meta">
				<span class="rating" role="img" aria-label={`Rated ${review.review.rating} out of 5 stars`}>
					{'★'.repeat(review.review.rating)}{'☆'.repeat(5 - review.review.rating)}
				</span>
				{#if review.review.createdAt}<span>{date.format(new Date(review.review.createdAt))}</span>{/if}
			</p>
			<p class="comment">{review.review.comment}</p>
			<ReviewPhotos photos={review.review.photos} />
			<a href={`/orders/${encodeURIComponent(review.review.orderId)}`}>Open the reviewed order</a>
		</div>
	{/if}
</section>

<style>
	.target { display: grid; gap: 0.5rem; }
	h2 { margin: 0; font-size: 1.1rem; }
	.loading { display: flex; justify-content: flex-start; }
	.card {
		display: grid;
		gap: 0.5rem;
		padding: 0.75rem 1rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
		background: var(--m3c-surface-container-low);
	}
	.card-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 0.5rem; }
	.badge {
		padding: 0.1rem 0.55rem;
		border-radius: 999px;
		font-size: 0.75rem;
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
	}
	.badge.removed { background: var(--m3c-error-container); color: var(--m3c-on-error-container); }
	dl { display: flex; flex-wrap: wrap; gap: 0.35rem 2rem; margin: 0; }
	dt { color: var(--m3c-on-surface-variant); font-size: 0.75rem; }
	dd { margin: 0.1rem 0 0; }
	.meta { display: flex; gap: 0.6rem; margin: 0; font-size: 0.875rem; color: var(--m3c-on-surface-variant); }
	.rating { color: var(--m3c-primary); }
	.comment { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
	p { margin: 0; }
</style>
