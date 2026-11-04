<script lang="ts">
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, reviewApi } from '$lib/services';
	import type { ReviewRecord } from '$lib/services/review/reviewApi';
	import ReviewPhotos from './ReviewPhotos.svelte';

	// "Reviews from other users" on the personal profile: the reviews the signed
	// in user received, newest first as the backend returns them.
	const shown = 3;
	let reviews = $state<ReviewRecord[] | null>(null);
	let error = $state('');
	let revision = $state(0);

	$effect(() => {
		const reviewer = authService.user?.id;
		void revision;
		if (!reviewer) return;
		let active = true;
		const lifetime = new AbortController();
		error = '';
		reviewApi
			.listForUser(reviewer, lifetime.signal)
			.then((page) => {
				if (active) reviews = page;
			})
			.catch(() => {
				if (active) error = 'We could not load your reviews. Check your connection and try again.';
			});
		return () => {
			active = false;
			lifetime.abort();
		};
	});

	const date = new Intl.DateTimeFormat('en-SG', { day: 'numeric', month: 'short', timeZone: 'Asia/Singapore' });
</script>

<Card variant="outlined">
	<section class="panel-body" aria-labelledby="reviews-title">
		<header class="head">
			<h2 id="reviews-title">Reviews from other users</h2>
		</header>
		{#if error}
			<p class="notice error" role="alert">{error}</p>
			<div><Button variant="outlined" onclick={() => revision++}>Try again</Button></div>
		{:else if !reviews}
			<div class="loading-state"><LoadingIndicator aria-label="Loading reviews" /></div>
		{:else if !reviews.length}
			<p class="muted">No reviews yet. Reviews you receive for completed requests appear here.</p>
		{:else}
			<ul>
				{#each reviews.slice(0, shown) as review (review.id)}
					<li>
						<div class="review-head">
							<span class="reviewer">{review.reviewerName || 'A user'}</span>
							<span class="rating" role="img" aria-label={`Rated ${review.rating} out of 5 stars`}>
								{'★'.repeat(review.rating)}{'☆'.repeat(5 - review.rating)}
							</span>
							{#if review.createdAt}<span class="date">{date.format(new Date(review.createdAt))}</span>{/if}
						</div>
						<p class="comment">{review.comment}</p>
						<ReviewPhotos photos={review.photos} />
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</Card>

<style>
	.head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
	h2 { margin: 0; font-size: 1.25rem; }
	.muted { margin: 0; color: var(--m3c-on-surface-variant); }
	.notice { margin: 0 0 0.75rem; }
	ul { display: grid; margin: 0; padding: 0; list-style: none; }
	li { display: grid; gap: 0.4rem; padding: 0.75rem 0; border-bottom: 1px solid var(--m3c-outline-variant); }
	li:last-child { border-bottom: 0; }
	.review-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.25rem 0.75rem; }
	.reviewer { font-weight: 600; }
	.rating { color: var(--m3c-primary); letter-spacing: 0.1rem; }
	.date { font-size: 0.85rem; color: var(--m3c-on-surface-variant); }
	.comment { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
