<script lang="ts">
	import { reviewApi } from '$lib/services';
	import ReviewListPage from '$lib/components/review/ReviewListPage.svelte';
	import ReviewRow from '$lib/components/review/ReviewRow.svelte';
</script>

<svelte:head><title>Your reviews · Friend on Campus</title></svelte:head>

<ReviewListPage
	title="Reviews from other users"
	emptyMessage="No reviews yet. Reviews you receive for completed requests appear here."
	load={(pagination, signal) => reviewApi.listReceived(pagination, signal)}
>
	{#snippet row(review)}
		<ReviewRow {review} counterpart={review.reviewerName} reportable />
	{/snippet}
</ReviewListPage>
