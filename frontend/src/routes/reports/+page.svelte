<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, reportApi } from '$lib/services';
	import { describeModerationError } from '$lib/services/moderation';
	import { reportCategoryLabels, reportOutcomeLabels, type ReportPage } from '$lib/services/report/reportApi';
	import CaseStatusChip from '$lib/components/moderation/CaseStatusChip.svelte';
	import Pagination from '$lib/components/admin/Pagination.svelte';

	let result = $state<ReportPage | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);

	const callerId = $derived(authService.user?.id ?? '');
	const currentPage = $derived(Math.max(1, Number(page.url.searchParams.get('page')) || 1));
	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium' });

	$effect(() => {
		const number = currentPage;
		void reload;
		let stale = false;
		loading = true;
		error = '';
		reportApi
			.listMyReports({ page: number })
			.then((value) => {
				if (!stale) result = value;
			})
			.catch((cause) => {
				if (!stale) error = describeModerationError(cause, 'load your reports');
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});
</script>

<svelte:head><title>Your reports · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="reports-title">
	<div><Button variant="text" href="/profile">← Your profile</Button></div>
	<header class="page-heading">
		<div>
			<p class="eyebrow">Your account</p>
			<h1 id="reports-title">Your reports</h1>
			<p>Reports you filed and reports about you. Report an order from its page, or a review from your reviews.</p>
		</div>
	</header>

	{#if loading && !result}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading reports" /><p>Loading your reports…</p></div>
	{:else if error}
		<Card variant="outlined">
			<div class="panel-body">
				<p class="notice error" role="alert">{error}</p>
				<div class="form-actions"><Button onclick={() => reload++}>Try again</Button></div>
			</div>
		</Card>
	{:else if result && result.reports.length === 0}
		<Card variant="outlined"><div class="panel-body"><p>You have no reports.</p></div></Card>
	{:else if result}
		<ul class="record-list">
			{#each result.reports as report (report.id)}
				{@const filed = report.reporterId === callerId}
				<li class="record-row">
					<div>
						<h2>
							<a href={`/reports/${report.id}`}>
								{filed ? `You reported ${report.subjectName || 'a user'}` : `${report.reporterName || 'A user'} reported you`}
							</a>
						</h2>
						<p>
							{reportCategoryLabels[report.category]} · {report.target.kind === 'order' ? 'Order' : 'Review'} ·
							{date.format(report.createdAt)}
							{#if report.outcome}· {reportOutcomeLabels[report.outcome]}{/if}
						</p>
						<CaseStatusChip status={report.status} />
					</div>
					<Button variant="outlined" href={`/reports/${report.id}`}>View</Button>
				</li>
			{/each}
		</ul>
		<Pagination
			page={result.page}
			totalPages={result.totalPages}
			{loading}
			onchange={(next) => void goto(`?page=${next}`, { keepFocus: true, noScroll: true })}
		/>
	{/if}
</section>
