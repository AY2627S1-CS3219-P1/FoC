<script lang="ts">
	import {
		reportCategoryLabels,
		reportOutcomeLabels,
		type ReportRecord
	} from '$lib/services/report/reportApi';
	import type { EvidenceRecord } from '$lib/services/moderation';
	import CaseStatusChip from './CaseStatusChip.svelte';
	import EvidenceGallery from './EvidenceGallery.svelte';

	// The body of a report, shared by the participant and administrator pages.
	let {
		report,
		refresh,
		callerId = ''
	}: {
		report: ReportRecord;
		refresh: (id: string) => Promise<EvidenceRecord>;
		callerId?: string;
	} = $props();

	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });
	const name = (id: string, display: string) => (id === callerId ? 'You' : display || 'A user');
</script>

<article class="summary" aria-labelledby="report-title">
	<header>
		<div>
			<p class="eyebrow">{reportCategoryLabels[report.category]}</p>
			<h1 id="report-title">
				{name(report.reporterId, report.reporterName)} reported {report.subjectId === callerId
					? 'you'
					: report.subjectName || 'a user'}
			</h1>
		</div>
		<CaseStatusChip status={report.status} />
	</header>

	<dl class="facts">
		<div>
			<dt>About</dt>
			<dd>
				{#if report.target.kind === 'order'}
					<a href={`/orders/${encodeURIComponent(report.target.orderId)}`}>An order</a>
				{:else}
					A review {report.subjectId === callerId ? 'you wrote' : `by ${report.subjectName || 'them'}`}
				{/if}
			</dd>
		</div>
		<div><dt>Filed</dt><dd>{date.format(report.createdAt)}</dd></div>
		{#if report.closedAt}<div><dt>Closed</dt><dd>{date.format(report.closedAt)}</dd></div>{/if}
	</dl>

	<section>
		<h2>What happened</h2>
		<p class="text">{report.description}</p>
		<EvidenceGallery evidence={report.evidence} {refresh} label="Evidence attached to this report" />
	</section>

	{#if report.outcome}
		<section class="outcome" aria-labelledby="report-outcome-title">
			<h2 id="report-outcome-title">Outcome: {reportOutcomeLabels[report.outcome]}</h2>
			<p class="text">{report.outcomeReason}</p>
			{#if report.closerName}<p class="secondary-text">Decided by {report.closerName}</p>{/if}
		</section>
	{/if}
</article>

<style>
	.summary { display: grid; gap: 1rem; }
	header { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 0.5rem 1rem; }
	h1 { margin: 0; }
	h2 { margin: 0 0 0.35rem; font-size: 1.1rem; }
	section { display: grid; gap: 0.5rem; }
	.facts { display: flex; flex-wrap: wrap; gap: 0.35rem 2.5rem; margin: 0; }
	.facts dt { color: var(--m3c-on-surface-variant); font-size: 0.75rem; }
	.facts dd { margin: 0.1rem 0 0; }
	.text { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
	.outcome {
		padding: 0.75rem 1rem;
		border-radius: var(--m3-shape-medium);
		background: var(--m3c-surface-container-low);
	}
</style>
