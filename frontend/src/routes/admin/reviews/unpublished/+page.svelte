<script lang="ts">
	import { onDestroy } from 'svelte';
	import { Button, Card, LoadingIndicator, TextFieldOutlined } from 'm3-svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { create } from '@bufbuild/protobuf';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { userSearchSchema } from '$lib/services/admin/adminSchemas';
	import { PaginationSchema } from '$lib/gen/pagination/v1/pagination_pb';
	import Pagination from '$lib/components/admin/Pagination.svelte';
	import UnpublishedReviewList from '$lib/components/review-moderation/UnpublishedReviewList.svelte';
	import { reviewModerationService, ReviewPageState, authService, adminService } from '$lib/services';

	const reviewPage = new ReviewPageState(reviewModerationService, () => authService.user?.id);
	const PAGE_SIZE = 10n;
	let account = authService.user?.id;
	$effect(() => {
		const next = authService.user?.id;
		if (next !== account) {
			clearReviewer();
		}
		account = next;
	});
	onDestroy(clearReviewer);

	let page = $state(1n);
	let submittedId = $state('');
	let inputError = $state('');
	let lookupRequest = 0;

	function clearReviewer(): void {
		lookupRequest++;
		reviewPage.clear();
		submittedId = '';
		inputError = '';
	}

	const { form: searchForm, errors: searchErrors, enhance, submitting } = superForm(defaults(zod4(userSearchSchema)), {
		SPA: true,
		validators: zod4Client(userSearchSchema),
		resetForm: false,
		multipleSubmits: 'prevent',
		onChange({ paths }) {
			if (paths.includes('email')) clearReviewer();
		},
		async onUpdate({ form }) {
			if (!form.valid) return;
			clearReviewer();
			const request = lookupRequest;
			const actor = authService.user?.id;
			const current = () => request === lookupRequest && actor === authService.user?.id;
			try {
				const reviewer = await adminService.getUserByEmail(form.data.email);
				if (!current()) return;
				submittedId = reviewer.id;
				await load(1n);
			} catch (cause) {
				if (!current()) return;
				inputError = cause instanceof ConnectError && cause.code === Code.NotFound
					? 'No account uses that email.'
					: cause instanceof ConnectError && cause.code === Code.PermissionDenied
						? 'You don\u2019t have access to this reviewer.'
						: 'We couldn\u2019t find this reviewer. Check your connection and try again.';
			}
		}
	});

	const records = $derived(reviewPage.records);
	const pageInfo = $derived(reviewPage.pageInfo);
	const isLoading = $derived(reviewPage.isLoading || $submitting);
	const hasLoaded = $derived(reviewPage.hasLoaded);
	const loadError = $derived(reviewPage.loadError);
	const cmsAvailable = $derived(reviewPage.cmsAvailable);
	const cmsError = $derived(reviewPage.cmsError);

	async function load(targetPage: bigint): Promise<void> {
		inputError = '';
		try {
			const applied = await reviewPage.loadReviews({
				reviewerId: submittedId,
				pagination: create(PaginationSchema, { page: targetPage, pageSize: PAGE_SIZE })
			});
			if (applied) page = targetPage;
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
				inputError = 'You don\u2019t have access to this reviewer.';
			}
		}
	}

	function changePage(target: number): void {
		if (submittedId) void load(BigInt(target));
	}

	function errorText(): string {
		const cause = loadError;
		if (cause instanceof ConnectError && cause.rawMessage.trim()) return cause.rawMessage;
		return 'We couldn\u2019t load unpublished reviews. Check your connection and try again.';
	}
</script>

<svelte:head><title>Unpublished reviews · Admin · Friend on Campus</title></svelte:head>

<section class="page-content admin-content" aria-labelledby="admin-unpublished-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1 id="admin-unpublished-title">Reviewer unpublished reviews</h1>
			<p>Search by email to see a reviewer&rsquo;s unpublished reviews.</p>
		</div>
	</header>

	<Card variant="outlined">
		<div class="panel-body">
			<form class="reviewer-form" method="POST" use:enhance>
				<TextFieldOutlined
					label="Reviewer email"
					name="email"
					type="email"
					required
					bind:value={$searchForm.email}
					error={!!inputError || !!$searchErrors.email}
				/>
				<Button type="submit" disabled={isLoading}>{$submitting ? 'Loading…' : 'Load reviews'}</Button>
			</form>
			{#if $searchErrors.email}<p class="notice error" role="alert">{$searchErrors.email}</p>{/if}
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
					{cmsError ?? 'Moderation progress is unavailable right now.'}
					The reviews below are unaffected.
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
				<Pagination
					page={Number(page)}
					totalPages={Number(pageInfo.totalPages)}
					loading={isLoading}
					onchange={changePage}
				/>
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
