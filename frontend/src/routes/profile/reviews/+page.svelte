<script lang="ts">
	import { page as appPage } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, reviewApi } from '$lib/services';
	import type { ReviewRecord } from '$lib/services/review/reviewApi';
	import Pagination from '$lib/components/pagination/Pagination.svelte';
	import ReceivedReview from '$lib/components/review/ReceivedReview.svelte';

	// Paginated list of reviews the signed-in user received.
	const pageSize = 10;
	let reviews = $state<ReviewRecord[] | null>(null);
	let currentPage = $state(1);
	let totalPages = $state(0);
	let loading = $state(false);
	let error = $state('');
	// Non-reactive guard: reading $state here would make the effect below
	// depend on `loading` and re-run itself forever.
	let inFlight = false;

	async function load(page: number) {
		const reviewer = authService.user?.id;
		if (!reviewer || inFlight) return;
		inFlight = true;
		loading = true;
		error = '';
		try {
			const result = await reviewApi.listForUser(
				reviewer,
				{ page, pageSize },
				AbortSignal.timeout(15000)
			);
			reviews = result.reviews;
			totalPages = result.totalPages;
			currentPage = page;
		} catch {
			error = 'We could not load your reviews. Check your connection and try again.';
		} finally {
			inFlight = false;
			loading = false;
		}
	}

	// The URL owns the page so refresh/back/forward land on the same one.
	const pageParam = $derived(Math.max(1, Number(appPage.url.searchParams.get('page') ?? '1') || 1));

	$effect(() => {
		const reviewer = authService.user?.id;
		const target = pageParam;
		if (!reviewer) return;
		void load(target);
	});

	function goToPage(next: number) {
		const url = new URL(appPage.url);
		url.searchParams.set('page', String(next));
		replaceState(url, {});
		document.getElementById('reviews-top')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
	}
</script>

<svelte:head><title>Your reviews · Friend on Campus</title></svelte:head>

<section class="reviews-page" id="reviews-top" aria-labelledby="reviews-title">
	<div class="top">
		<Button variant="text" href="/profile">Back to profile</Button>
	</div>

	<Card variant="outlined">
		<div class="panel-body">
			<header class="head">
				<h1 id="reviews-title">Reviews from other users</h1>
			</header>

			{#if !authService.user}
				<p class="muted"><a href="/login">Sign in</a> to see your reviews.</p>
			{:else if error}
				<p class="notice error" role="alert">{error}</p>
				<div><Button variant="outlined" onclick={() => load(currentPage)}>Try again</Button></div>
			{:else if !reviews}
				<div class="loading-state"><LoadingIndicator aria-label="Loading reviews" /></div>
			{:else if !reviews.length}
				<p class="muted">No reviews yet. Reviews you receive for completed requests appear here.</p>
			{:else}
				<ul>
					{#each reviews as review (review.id)}
						<ReceivedReview {review} />
					{/each}
				</ul>
				<Pagination page={currentPage} {totalPages} {loading} onchange={goToPage} />
			{/if}
		</div>
	</Card>
</section>

<style>
	.reviews-page { display: grid; gap: 1rem; max-width: 48rem; margin: 0 auto; scroll-margin-top: 1rem; }
	.top { display: flex; justify-content: space-between; gap: 0.5rem; }
	.head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
	h1 { margin: 0; font-size: 1.35rem; }
	.muted { margin: 0; color: var(--m3c-on-surface-variant); }
	.notice { margin: 0 0 0.75rem; }
	ul { display: grid; margin: 0; padding: 0; list-style: none; }
	.loading-state { display: grid; place-items: center; padding: 2rem; }
</style>
