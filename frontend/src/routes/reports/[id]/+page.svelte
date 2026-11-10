<script lang="ts">
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService, reportApi } from '$lib/services';
	import { describeModerationError } from '$lib/services/moderation';
	import { appealableDecision, type ReportDetail } from '$lib/services/report/reportApi';
	import ReportSummary from '$lib/components/moderation/ReportSummary.svelte';
	import ResponseThread from '$lib/components/moderation/ResponseThread.svelte';
	import ResponseForm from '$lib/components/moderation/ResponseForm.svelte';

	let detail = $state<ReportDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);

	const callerId = $derived(authService.user?.id ?? '');
	const report = $derived(detail?.report ?? null);
	// Only the reported participant responds, until the report closes.
	const canRespond = $derived(!!report && report.subjectId === callerId && report.status !== 'closed');
	const awaited = $derived(report?.status === 'awaiting_response' && report.subjectId === callerId);
	const appealable = $derived(report ? appealableDecision(report, callerId) : null);
	const appealHref = $derived(
		appealable
			? `/appeals/new?${appealable.kind === 'report' ? 'reportId' : 'retainedReviewId'}=${encodeURIComponent(appealable.id)}`
			: ''
	);

	const refresh = (id: string) => reportApi.getEvidenceDownload(id);

	$effect(() => {
		const id = page.params.id ?? '';
		void reload;
		let stale = false;
		loading = true;
		error = '';
		reportApi
			.getReport(id)
			.then((value) => {
				if (!stale) detail = value;
			})
			.catch((cause) => {
				if (!stale) {
					detail = null;
					error = describeModerationError(cause, 'load this report');
				}
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});

	async function respond(body: string, evidenceIds: string[]) {
		if (!report) return;
		await reportApi.submitResponse(report.id, body, evidenceIds);
		reload++;
	}
</script>

<svelte:head><title>Report · Friend on Campus</title></svelte:head>

<section class="page-content">
	<div><Button variant="text" href="/reports">← Your reports</Button></div>

	{#if loading && !detail}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading report" /><p>Loading report…</p></div>
	{:else if error}
		<Card variant="outlined">
			<div class="panel-body">
				<p class="notice error" role="alert">{error}</p>
				<div class="form-actions"><Button onclick={() => reload++}>Try again</Button></div>
			</div>
		</Card>
	{:else if detail && report}
		{#if awaited}
			<p class="notice" role="status">An administrator has asked for your side of this report.</p>
		{/if}
		<Card variant="outlined">
			<div class="panel-body"><ReportSummary {report} {refresh} {callerId} /></div>
		</Card>
		{#if appealable}
			<Card variant="outlined">
				<div class="panel-body appeal">
					<p>If you disagree with this outcome, you can appeal it once.</p>
					<Button variant="tonal" href={appealHref}>Appeal this outcome</Button>
				</div>
			</Card>
		{/if}
		<Card variant="outlined">
			<div class="panel-body"><ResponseThread responses={detail.responses} {refresh} {callerId} /></div>
		</Card>
		{#if canRespond}
			<Card variant="outlined">
				<div class="panel-body">
					<ResponseForm
						heading="Respond to this report"
						help="Your response is shared with the reporter and the administrators reviewing it."
						upload={(input, signal) => reportApi.createEvidenceUpload(input, signal)}
						onsubmit={respond}
					/>
				</div>
			</Card>
		{/if}
	{/if}
</section>

<style>
	.appeal { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 0.75rem; }
	.appeal p { margin: 0; }
</style>
