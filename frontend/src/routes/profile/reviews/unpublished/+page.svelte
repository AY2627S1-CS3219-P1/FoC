<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { ModerationStatus } from '$lib/gen/user/review/v1/review_pb';
	import { create } from '@bufbuild/protobuf';
	import { PaginationSchema } from '$lib/gen/pagination/v1/pagination_pb';
	import Pagination from '$lib/components/admin/Pagination.svelte';
	import UnpublishedReviewList from '$lib/components/review-moderation/UnpublishedReviewList.svelte';
	import { authService, reviewModerationService } from '$lib/services';

	// The reviewer's own unpublished reviews: pending, rejected and
	// moderation-error. Publication is authoritative in User Service; CMS job
	// progress only explains what is happening behind a pending review. The
	// page polls bounded while open; no SSE.
	const PAGE_SIZE = 10n;
	const POLL_INTERVAL_MS = 20000;

	let page = $state(1n);
	let statusFilter = $state<ModerationStatus | undefined>(undefined);
	let pollTimer: ReturnType<typeof setInterval> | undefined;

	const records = $derived(reviewModerationService.records);
	const pageInfo = $derived(reviewModerationService.pageInfo);
	const isLoading = $derived(reviewModerationService.isLoading);
	const hasLoaded = $derived(reviewModerationService.hasLoaded);
	const loadError = $derived(reviewModerationService.loadError);
	const cmsAvailable = $derived(reviewModerationService.cmsAvailable);
	const cmsError = $derived(reviewModerationService.cmsError);

	function statusOptions() {
		return [
			{ value: undefined, label: 'All unpublished' },
			{ value: ModerationStatus.PENDING_MODERATION, label: 'Pending' },
			{ value: ModerationStatus.REJECTED, label: 'Rejected' },
			{ value: ModerationStatus.MODERATION_ERROR, label: 'Assessment error' }
		];
	}

	async function load(targetPage: bigint = page): Promise<void> {
		try {
			await reviewModerationService.loadReviews({
				reviewerId: authService.user!.id,
				moderationStatusFilter: statusFilter,
				pagination: create(PaginationSchema, { page: targetPage, pageSize: PAGE_SIZE })
			});
			page = targetPage;
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
				// A stale role claim; nothing to retry here.
				return;
			}
			// Other failures are reflected by loadError for the retry state.
		}
	}

	function changePage(target: number): void {
		void load(BigInt(target));
	}

	function changeFilter(event: Event): void {
		const value = (event.currentTarget as HTMLSelectElement).value;
		statusFilter = value === '' ? undefined : (Number(value) as ModerationStatus);
		page = 1n;
		void load(1n);
	}

	function startPolling(): void {
		stopPolling();
		pollTimer = setInterval(() => {
			if (!isLoading) void load();
		}, POLL_INTERVAL_MS);
	}

	function stopPolling(): void {
		if (pollTimer) clearInterval(pollTimer);
		pollTimer = undefined;
	}

	function errorText(): string {
		const cause = loadError;
		if (cause instanceof ConnectError && cause.rawMessage.trim()) return cause.rawMessage;
		return 'We could not load your unpublished reviews. Check your connection and try again.';
	}

	onMount(() => {
		void (async () => {
			if (!authService.isAuthenticated) {
				await goto('/login', { replaceState: true });
				return;
			}
			await load(1n);
			startPolling();
		})();
		return stopPolling;
	});
</script>

<svelte:head><title>Unpublished reviews · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="unpublished-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Your account</p>
			<h1 id="unpublished-title">Unpublished reviews</h1>
			<p>Reviews waiting for assessment, rejected, or whose assessment failed. Published reviews live in your normal reviews list.</p>
		</div>
	</header>

	{#if !hasLoaded && isLoading}
		<div class="loading-state" role="status" aria-live="polite">
			<LoadingIndicator aria-label="Loading unpublished reviews" />
			<p>Loading your unpublished reviews…</p>
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
				Your reviews below are still safe; progress will reappear when the service returns.
			</p>
		{/if}
		<div class="list-controls">
			<label class="filter-label">
				Show
				<select class="filter-select" value={statusFilter ?? ''} onchange={changeFilter}>
					{#each statusOptions() as option (option.label)}
						<option value={option.value === undefined ? '' : String(option.value)}>{option.label}</option>
					{/each}
				</select>
			</label>
			<Button variant="text" disabled={isLoading} onclick={() => void load()}>Refresh</Button>
		</div>

		{#if records.length === 0}
			<Card variant="outlined">
				<div class="panel-body">
					<p class="notice" role="status">
						{statusFilter === undefined
							? 'You have no unpublished reviews. Reviews appear here while they await assessment, or if they cannot be published.'
							: 'No unpublished reviews match this filter.'}
					</p>
				</div>
			</Card>
		{:else}
			<UnpublishedReviewList {records} />
		{/if}

		{#if pageInfo}
			<Pagination page={Number(page)} totalPages={Number(pageInfo.totalPages)} loading={isLoading} onchange={changePage} />
		{/if}
	{/if}
</section>
