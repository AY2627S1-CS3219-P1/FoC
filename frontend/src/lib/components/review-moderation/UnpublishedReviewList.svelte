<script lang="ts">
	import { Card } from 'm3-svelte';
	import ReviewStatusChip from './ReviewStatusChip.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Timestamp } from '@bufbuild/protobuf/wkt';
	import type { ReviewModerationRecord } from '$lib/services/review-moderation/reviewModerationApi';
	import { presentReviewModeration } from '$lib/services/review-moderation/presentation';

	let { records }: { records: readonly ReviewModerationRecord[] } = $props();


	function stars(rating: number): string {
		const clamped = Math.min(5, Math.max(0, rating));
		return '★'.repeat(clamped) + '☆'.repeat(5 - clamped);
	}

	// Card dates read as "09 Nov 2026, 3:04 pm"; the clock stays out of seconds.
	function formatDate(value: Timestamp | undefined): string {
		if (!value) return '—';
		const date = timestampDate(value);
		const day = String(date.getDate()).padStart(2, '0');
		const month = date.toLocaleString('en-SG', { month: 'short' });
		const time = date
			.toLocaleTimeString('en-SG', { hour: 'numeric', minute: '2-digit', hour12: true })
			.toLowerCase()
			.replace(/\u202f/g, ' ');
		return `${day} ${month} ${date.getFullYear()}, ${time}`;
	}
</script>

<ul class="unpublished-list" aria-label="Unpublished reviews">
	{#each records as record (record.review.id)}
		{@const view = presentReviewModeration(record)}
		<li>
			<Card variant="outlined">
				<div class="review-body" data-tone={view.tone}>
					<header class="review-heading">
						<ReviewStatusChip label={view.label} tone={view.tone} />
						<span class="review-meta">
							<span class="rating-stars" role="img" aria-label={`Rated ${record.review.rating} out of 5`}>{stars(record.review.rating)}</span>
							<span>{formatDate(record.review.createdAt)}</span>
						</span>
						{#if record.review.orderId}
							<a class="order-link" href={`/orders/${record.review.orderId}`}>View order</a>
						{/if}
					</header>
					<p class="review-comment">{record.review.comment}</p>
					{#if view.detail}<p class="review-detail" role="status">{view.detail}</p>{/if}
				</div>
			</Card>
		</li>
	{/each}
</ul>

<style>
	.unpublished-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 1rem;
	}
	.review-body {
		display: grid;
		gap: 0.5rem;
	}
	.review-heading {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex-wrap: wrap;
	}
	.review-meta {
		color: var(--md-sys-color-on-surface-variant, #5f6368);
		font-size: 0.875rem;
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}
	.rating-stars {
		color: var(--m3c-primary);
		letter-spacing: 0.1rem;
	}
	.order-link {
		margin-inline-start: auto;
		color: var(--m3c-primary);
		font-size: 0.875rem;
	}
	.review-comment {
		margin: 0;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.review-detail {
		margin: 0;
		font-size: 0.9rem;
		color: var(--md-sys-color-on-surface-variant, #5f6368);
	}
</style>
