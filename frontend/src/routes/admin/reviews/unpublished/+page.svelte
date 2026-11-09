<script lang="ts">
	import { Button, Card, LoadingIndicator, TextFieldOutlined } from 'm3-svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { create } from '@bufbuild/protobuf';
	import { ModerationStatus } from '$lib/gen/user/review/v1/review_pb';
	import { PaginationSchema } from '$lib/gen/pagination/v1/pagination_pb';
	import Pagination from '$lib/components/admin/Pagination.svelte';
	import UnpublishedReviewList from '$lib/components/review-moderation/UnpublishedReviewList.svelte';
	import { reviewModerationService } from '$lib/services';

	// Admin inspection of one reviewer's unpublished reviews. The reviewer ID is
	// resolved by the backend's authorization, never by this page: an ordinary
	// caller id entered here simply fails with permission_denied.
	const PAGE_SIZE = 10n;

	let reviewerId = $state('');
	let page = $state(1n);
	let submittedId = $state('');
	let inputError = $state('');

	const records = $derived(reviewModerationService.records);
	const pageInfo = $derived(reviewModerationService.pageInfo);
	const isLoading = $derived(reviewModerationService.isLoading);
	const hasLoaded = $derived(reviewModerationService.hasLoaded);
	const loadError = $derived(reviewModerationService.loadError);
	const cmsAvailable = $derived(reviewModerationService.cmsAvailable);
	const cmsError = $derived(reviewModerationService.cmsError);

	const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

	async function load(targetPage: bigint): Promise<void> {
		inputError = '';
		try {
			await reviewModerationService.loadReviews({
				reviewerId: submittedId,
				pagination: create(PaginationSchema, { page: targetPage, pageSize: PAGE_SIZE })
			});
			page = targetPage;
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
				inputError = 'You are not allowed to inspect this reviewer.';
			}
			// Other failures surface through loadError below.
		}
	}

	function submit(event: SubmitEvent): void {
		event.preventDefault();
		const candidate = reviewerId.trim();
		if (!UUID_PATTERN.test(candidate)) {
			inputError = 'Enter a valid reviewer UUID.';
			return;
		}
		submittedId = candidate;
		reviewModerationService.clear();
		void load(1n);
	}

	function changePage(target: number): void {
		if (submittedId) void load(BigInt(target));
	}

	function errorText(): string {
		const cause = loadError;
		if (cause instanceof ConnectError && cause.rawMessage.trim()) return cause.rawMessage;
		return 'We could not load the unpublished reviews. Check your connection and try again.';
	}
</script>

<svelte:head><title>Unpublished reviews · Admin · Friend on Campus</title></svelte:head>

<section class="page-content admin-content" aria-labelledby="admin-unpublished-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1 id="admin-unpublished-title">Reviewer unpublished reviews</h1>
			<p>Inspect a reviewer's pending, rejected and moderation-error reviews.</p>
		</div>
	</header>

	<Card variant="outlined">
		<div class="panel-body">
			<form class="reviewer-form" onsubmit={submit}>
				<TextFieldOutlined
					label="Reviewer UUID"
					name="reviewerId"
					required
					placeholder="00000000-0000-0000-0000-000000000000"
					bind:value={reviewerId}
					error={!!inputError}
				/>
				<Button type="submit" disabled={isLoading}>Load reviews</Button>
			</form>
			{#if inputError}<p class="notice error" role="alert">{inputError}</p>{/if}
		</div>
	</Card>

	{#if submittedId}
		{#if !hasLoaded && isLoading}
			<div class="loading-state" role="status" aria-live="polite">
				<LoadingIndicator aria-label="Loading unpublished reviews" />
				<p>Loading reviews…</p>
			</div>
		{:else if loadError}
			<Card variant="outlined">
				<div class="panel-body">
					<p class="notice error" role="alert">{errorText()}</p>
					<div class="form-actions">
						<Button onclick={() => void load(1n)}>Try again</Button>
					</div>
				</div>
			</Card>
		{:else}
			{#if !cmsAvailable}
				<p class="notice" role="status">
					{cmsError ?? 'Moderation progress is temporarily unavailable.'}
					The reviews below are still listed; grading progress will reappear when the service returns.
				</p>
			{/if}
			{#if records.length === 0}
				<Card variant="outlined">
					<div class="panel-body">
						<p class="notice" role="status">This reviewer has no unpublished reviews.</p>
					</div>
				</Card>
			{:else}
				<UnpublishedReviewList {records} />
			{/if}
			{#if pageInfo}
				<Pagination page={Number(page)} totalPages={Number(pageInfo.totalPages)} loading={isLoading} onchange={changePage} />
			{/if}
		{/if}
	{/if}
</section>

<style>
	.reviewer-form {
		display: flex;
		gap: 1rem;
		align-items: end;
		flex-wrap: wrap;
	}
</style>
