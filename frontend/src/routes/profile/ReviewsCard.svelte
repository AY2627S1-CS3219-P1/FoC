<script lang="ts">
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, reviewApi } from '$lib/services';
	import type { ReviewRecord } from '$lib/services/review/reviewApi';
	import ReviewRow from '$lib/components/review/ReviewRow.svelte';
	// Profile preview of received reviews: the two newest, with a link to the
	// paginated page for the rest.
	const shown = 2;
	let reviews = $state<ReviewRecord[] | null>(null);
	let error = $state('');
	let revision = $state(0);

	$effect(() => {
		const reviewer = authService.user?.id;
		void revision;
		if (!reviewer) return;
		let active = true;
		error = '';
		reviews = null;
		reviewApi
			.listForUser(reviewer, { page: 1, pageSize: shown }, AbortSignal.timeout(15000))
			.then((result) => {
				if (active) reviews = result.reviews;
			})
			.catch(() => {
				if (active) error = 'We could not load your reviews. Check your connection and try again.';
			});
		return () => (active = false);
	});
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
				{#each reviews as review (review.id)}
					<ReviewRow {review} counterpart={review.reviewerName} />
				{/each}
			</ul>
		{/if}
		<div class="more">
			<Button variant="text" href="/profile/reviews">View All Reviews</Button>
			<Button variant="text" href="/profile/reviews/given">View Reviews I Left</Button>
		</div>
	</section>
</Card>

<style>
	.head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
	h2 { margin: 0; font-size: 1.25rem; }
	.muted { margin: 0; color: var(--m3c-on-surface-variant); }
	.notice { margin: 0 0 0.75rem; }
	ul { display: grid; margin: 0; padding: 0; list-style: none; }
	.more { display: flex; flex-wrap: wrap; justify-content: flex-start; gap: 0.25rem; }
</style>
