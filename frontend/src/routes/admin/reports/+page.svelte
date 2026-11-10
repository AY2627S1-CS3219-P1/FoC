<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, LoadingIndicator, SelectOutlined } from 'm3-svelte';
	import { reportApi } from '$lib/services';
	import { CASE_STATUSES, caseStatusLabels, describeModerationError, type CaseStatus } from '$lib/services/moderation';
	import { reportCategoryLabels, type ReportPage } from '$lib/services/report/reportApi';
	import CaseStatusChip from '$lib/components/moderation/CaseStatusChip.svelte';
	import Pagination from '$lib/components/admin/Pagination.svelte';

	let result = $state<ReportPage | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);

	// The queue opens on new reports; "all" lists every status.
	const statusOptions = [
		...CASE_STATUSES.map((status) => ({ value: status, text: caseStatusLabels[status] })),
		{ value: 'all', text: 'All statuses' }
	];
	const status = $derived.by((): CaseStatus | 'all' => {
		const value = page.url.searchParams.get('status');
		return value === 'all' || CASE_STATUSES.includes(value as CaseStatus) ? (value as CaseStatus | 'all') : 'open';
	});
	const currentPage = $derived(Math.max(1, Number(page.url.searchParams.get('page')) || 1));
	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });

	$effect(() => {
		const filter = status;
		const number = currentPage;
		void reload;
		let stale = false;
		loading = true;
		error = '';
		reportApi
			.listReports({ status: filter === 'all' ? undefined : filter, page: number })
			.then((value) => {
				if (!stale) result = value;
			})
			.catch((cause) => {
				if (!stale) error = describeModerationError(cause, 'load reports');
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});

	function update(nextStatus: string = status, nextPage = 1) {
		void goto(`?status=${nextStatus}&page=${nextPage}`, { keepFocus: true, noScroll: true });
	}
</script>

<svelte:head><title>Reports · Friend on Campus</title></svelte:head>
<section class="page-content admin-content" aria-labelledby="reports-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1 id="reports-title">Reports</h1>
			<p>Review reports about orders and reviews.</p>
		</div>
		<Button variant="outlined" disabled={loading} onclick={() => reload++}>Refresh queue</Button>
	</header>
	<div class="field">
		<SelectOutlined
			label="Report status"
			options={statusOptions}
			value={status}
			onchange={(event) => update(event.currentTarget.value)}
		/>
	</div>
	{#if error}
		<p class="notice error" role="alert">{error}</p>
		<Button onclick={() => reload++}>Retry reports</Button>
	{:else if loading && !result}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading reports" /><p>Loading reports…</p></div>
	{:else if result}
		<p class="secondary-text" role="status">{result.totalItems} reports</p>
		{#if !result.reports.length}<p class="notice" role="status">No reports on this page.</p>{/if}
		<ul class="record-list">
			{#each result.reports as report (report.id)}
				<li class="record-row">
					<div>
						<h2>
							<a href={`/admin/reports/${report.id}`}>
								{report.reporterName || 'A user'} → {report.subjectName || 'a user'}
							</a>
						</h2>
						<p>
							{reportCategoryLabels[report.category]} · {report.target.kind === 'order' ? 'Order' : 'Review'} ·
							{date.format(report.createdAt)}
						</p>
						<CaseStatusChip status={report.status} />
					</div>
					<Button variant="outlined" href={`/admin/reports/${report.id}`}>
						{report.status === 'closed' ? 'View' : 'Review'}
					</Button>
				</li>
			{/each}
		</ul>
		<Pagination page={result.page} totalPages={result.totalPages} {loading} onchange={(next) => update(status, next)} />
	{/if}
</section>
