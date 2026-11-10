<script lang="ts">
	import { onMount } from 'svelte';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { create } from '@bufbuild/protobuf';
	import { PaginationSchema } from '$lib/gen/pagination/v1/pagination_pb';
	import Pagination from '$lib/components/admin/Pagination.svelte';
	import UnpublishedReviewList from '$lib/components/review-moderation/UnpublishedReviewList.svelte';
	import {
		authService,
		profileService,
		reviewModerationService,
		ReviewPageState
	} from '$lib/services';

	const reviewPage = new ReviewPageState(reviewModerationService, () => authService.user?.id);
	const PAGE_SIZE = 10n;
	let ownerId = '';
	let mounted = false;
	let initialization = 0;
	let account = authService.user?.id;
	let bootstrapLoading = $state(true);
	let bootstrapError = $state<unknown>(null);
	$effect(() => {
		const next = authService.user?.id;
		if (next !== account) {
			initialization += 1;
			reviewPage.clear();
			ownerId = '';
			stopPolling();
			bootstrapError = null;
			bootstrapLoading = false;
			if (mounted && next) void initialize();
		}
		account = next;
	});
	const POLL_INTERVAL_MS = 20000;

	let page = $state(1n);
	let pollTimer: ReturnType<typeof setInterval> | undefined;

	const records = $derived(reviewPage.records);
	const pageInfo = $derived(reviewPage.pageInfo);
	const isLoading = $derived(bootstrapLoading || reviewPage.isLoading);
	const hasLoaded = $derived(reviewPage.hasLoaded);
	const loadError = $derived(bootstrapError ?? reviewPage.loadError);
	const cmsAvailable = $derived(reviewPage.cmsAvailable);
	const cmsError = $derived(reviewPage.cmsError);

	async function load(targetPage: bigint = page): Promise<void> {
		if (!ownerId) {
			await initialize();
			return;
		}
		try {
			const applied = await reviewPage.loadReviews({
				reviewerId: ownerId,
				pagination: create(PaginationSchema, { page: targetPage, pageSize: PAGE_SIZE })
			});
			if (applied) page = targetPage;
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
				// Transport owns session refresh; do not compensate for stale role claims here.
				return;
			}
		}
	}

	function changePage(target: number): void {
		void load(BigInt(target));
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
		return 'We couldn\u2019t load your unpublished reviews. Check your connection and try again.';
	}

	async function initialize(): Promise<void> {
		const request = ++initialization;
		bootstrapLoading = true;
		bootstrapError = null;
		try {
			const profile = await profileService.getMyProfile();
			if (!mounted || request !== initialization) return;
			ownerId = profile.id;
			await load(1n);
			if (mounted && request === initialization) startPolling();
		} catch (error) {
			if (mounted && request === initialization) bootstrapError = error;
		} finally {
			if (mounted && request === initialization) bootstrapLoading = false;
		}
	}
	onMount(() => {
		mounted = true;
		void initialize();
		return () => {
			mounted = false;
			initialization += 1;
			stopPolling();
			reviewPage.clear();
		};
	});
</script>

<svelte:head><title>Unpublished reviews · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="unpublished-title">
	<div class="back-link">
		<Button variant="text" href="/profile/reviews/given">Back to my reviews</Button>
	</div>
	<header class="page-heading">
		<div>
			<p class="eyebrow">Your account</p>
			<h1 id="unpublished-title">Unpublished reviews</h1>
			<p>
				Reviews are published to the order once they&rsquo;re approved. Everything else stays here.
			</p>
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
				{cmsError ?? 'Moderation progress is unavailable right now.'}
				The reviews below are unaffected.
			</p>
		{/if}
		<div class="list-controls">
			<Button variant="text" disabled={isLoading} onclick={() => void load()}>Refresh</Button>
		</div>

		{#if records.length === 0}
			<Card variant="outlined">
				<div class="panel-body">
					<p class="notice" role="status">You have no unpublished reviews.</p>
				</div>
			</Card>
		{:else}
			<UnpublishedReviewList {records} />
		{/if}

		{#if pageInfo}
			<Pagination
				page={Number(page)}
				totalPages={Number(pageInfo.totalPages)}
				loading={isLoading}
				onchange={changePage}
			/>
		{/if}
	{/if}
</section>
