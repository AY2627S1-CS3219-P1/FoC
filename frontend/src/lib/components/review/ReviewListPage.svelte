<script lang="ts">
	import { page as appPage } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import type { Snippet } from 'svelte';
	import type { ReviewRecord, ReviewPage } from '$lib/services/review/reviewApi';
	import Pagination from '$lib/components/admin/Pagination.svelte';

	// Paginated review list shared by the received and written review pages.
	// The URL owns the page so refresh/back/forward land on the same one.
	let {
		title,
		emptyMessage,
		load,
		row
	}: {
		title: string;
		emptyMessage: string;
		load: (
			pagination: { page: number; pageSize: number },
			signal: AbortSignal
		) => Promise<ReviewPage>;
		row: Snippet<[ReviewRecord]>;
	} = $props();

	const pageSize = 10;
	let reviews = $state<ReviewRecord[] | null>(null);
	let currentPage = $state(1);
	let totalPages = $state(0);
	let loading = $state(false);
	let error = $state('');
	// Non-reactive guard: reading $state here would make the effect below
	// depend on `loading` and re-run itself forever.
	let inFlight = false;

	async function fetchPage(page: number) {
		if (inFlight) return;
		inFlight = true;
		loading = true;
		error = '';
		const signal = AbortSignal.timeout(15000);
		try {
			let result = await load({ page, pageSize }, signal);
			let shown = page;
			// A bookmarked page can outlive its reviews. Clamp to the last page
			// instead of showing an empty list with no way back.
			if (result.reviews.length === 0 && result.totalPages > 0 && page > result.totalPages) {
				shown = result.totalPages;
				result = await load({ page: shown, pageSize }, signal);
				syncPageParam(shown);
			}
			reviews = result.reviews;
			totalPages = result.totalPages;
			currentPage = shown;
		} catch {
			error = 'We could not load these reviews. Check your connection and try again.';
		} finally {
			inFlight = false;
			loading = false;
		}
	}

	const pageParam = $derived(Math.max(1, Number(appPage.url.searchParams.get('page') ?? '1') || 1));

	// The protected transport handles refresh and the login redirect; pages
	// never inspect the local auth state.
	$effect(() => {
		const target = pageParam;
		void fetchPage(target);
	});

	function syncPageParam(page: number) {
		const url = new URL(appPage.url);
		url.searchParams.set('page', String(page));
		replaceState(url, {});
	}

	function goToPage(next: number) {
		// Fetch now instead of waiting for the URL change to re-run the effect;
		// the in-flight guard makes the effect's follow-up a no-op.
		void fetchPage(next);
		syncPageParam(next);
		document.getElementById('reviews-top')?.scrollIntoView({ behavior: 'instant', block: 'start' });
	}
</script>

<section class="reviews-page" id="reviews-top" aria-labelledby="reviews-title">
	<div class="top">
		<Button variant="text" href="/profile">Back to profile</Button>
	</div>

	<Card variant="outlined">
		<div class="panel-body">
			<header class="head">
				<h1 id="reviews-title">{title}</h1>
			</header>

			{#if error}
				<p class="notice error" role="alert">{error}</p>
				<div><Button variant="outlined" onclick={() => fetchPage(currentPage)}>Try again</Button></div>
			{:else if !reviews}
				<div class="loading-state"><LoadingIndicator aria-label="Loading reviews" /></div>
			{:else if !reviews.length}
				<p class="muted">
					{totalPages > 0 ? 'No reviews on this page.' : emptyMessage}
				</p>
				{#if totalPages > 0}
					<Pagination page={currentPage} {totalPages} {loading} onchange={goToPage} />
				{/if}
			{:else}
				<ul>
					{#each reviews as review (review.id)}
						{@render row(review)}
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
