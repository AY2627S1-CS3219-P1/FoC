<script lang="ts">
	import type { ReviewRecord } from '$lib/services/review/reviewApi';
	import ReviewPhotos from './ReviewPhotos.svelte';

	// One review row, shared by the profile card and both paginated review
	// pages. `counterpart` names the other party in the row's heading.
	// `reportable` offers a report link; set it only on reviews the caller received.
	let {
		review,
		counterpart,
		orderLink = false,
		reportable = false
	}: { review: ReviewRecord; counterpart: string; orderLink?: boolean; reportable?: boolean } = $props();
	const date = new Intl.DateTimeFormat('en-SG', {
		day: 'numeric',
		month: 'short',
		timeZone: 'Asia/Singapore'
	});
</script>

<li class="review">
	<div class="review-head">
		<span class="reviewer">{counterpart || 'A user'}</span>
		<div class="meta">
			<span class="rating" role="img" aria-label={`Rated ${review.rating} out of 5 stars`}>
				{'★'.repeat(review.rating)}{'☆'.repeat(5 - review.rating)}
			</span>
			{#if review.createdAt}<span class="date">{date.format(new Date(review.createdAt))}</span>{/if}
			{#if orderLink}
				<a class="order-link" href={`/orders/${review.orderId}`}>Go to Order</a>
			{/if}
			{#if reportable}
				<a class="order-link" href={`/reports/new?reviewId=${encodeURIComponent(review.id)}`}>Report</a>
			{/if}
		</div>
	</div>
	<p class="comment">{review.comment}</p>
	<div class="review-photos"><ReviewPhotos photos={review.photos} /></div>
</li>

<style>
	.review { display: grid; gap: 0.4rem; padding: 1.1rem 0; border-bottom: 1px solid var(--m3c-outline-variant); }
	.review:last-child { border-bottom: 0; }
	.review-head { display: grid; gap: 0.15rem; }
	.meta { display: flex; flex-wrap: wrap; align-items: center; gap: 0.25rem 0.6rem; }
	.reviewer { font-weight: 600; }
	.rating { color: var(--m3c-primary); letter-spacing: 0.1rem; }
	.date { font-size: 0.85rem; color: var(--m3c-on-surface-variant); }
	.order-link { font-size: 0.85rem; color: var(--m3c-primary); }
	.comment { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
	.review-photos { margin-top: 0.4rem; }
</style>
