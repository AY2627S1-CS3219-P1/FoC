<script lang="ts">
	import { Button } from 'm3-svelte';
	import { reviewApi } from '$lib/services';
	import ReviewListPage from '$lib/components/review/ReviewListPage.svelte';
	import ReviewRow from '$lib/components/review/ReviewRow.svelte';
</script>

<svelte:head><title>Reviews you left · Friend on Campus</title></svelte:head>

<ReviewListPage
	title="Reviews you left"
	emptyMessage="No reviews yet. Reviews you leave for completed requests appear here."
	load={(pagination, signal) => reviewApi.listMine(pagination, signal)}
>
	{#snippet headerActions()}
		<Button variant="outlined" href="/profile/reviews/unpublished">View unpublished reviews</Button>
	{/snippet}
	{#snippet row(review)}
		<ReviewRow {review} counterpart={review.revieweeName} orderLink />
	{/snippet}
</ReviewListPage>
