<script lang="ts">
	import {
		decisionKindLabels,
		decisionOutcomeLabels,
		type AppealRecord,
		type DecisionTarget
	} from '$lib/services/appeal/appealApi';
	import type { EvidenceRecord } from '$lib/services/moderation';
	import CaseStatusChip from './CaseStatusChip.svelte';
	import EvidenceGallery from './EvidenceGallery.svelte';

	// The body of an appeal, shared by the participant and administrator pages.
	// `reportHref` points a report decision at the page the viewer can open.
	let {
		appeal,
		refresh,
		callerId = '',
		reportHref = (id: string) => `/reports/${encodeURIComponent(id)}`
	}: {
		appeal: AppealRecord;
		refresh: (id: string) => Promise<EvidenceRecord>;
		callerId?: string;
		reportHref?: (id: string) => string;
	} = $props();

	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });

	function targetHref(target: DecisionTarget): string | null {
		return target.kind === 'report' ? reportHref(target.id) : null;
	}
</script>

<article class="summary" aria-labelledby="appeal-title">
	<header>
		<div>
			<p class="eyebrow">Appeal</p>
			<h1 id="appeal-title">
				{appeal.appellantId === callerId ? 'Your appeal' : `Appeal by ${appeal.appellantName || 'a user'}`}
			</h1>
		</div>
		<CaseStatusChip status={appeal.status} />
	</header>

	<dl class="facts">
		{#if appeal.requestId}
			<div><dt>Order</dt><dd><a href={`/orders/${encodeURIComponent(appeal.requestId)}`}>View order</a></dd></div>
		{/if}
		<div><dt>Filed</dt><dd>{date.format(appeal.createdAt)}</dd></div>
		{#if appeal.closedAt}<div><dt>Closed</dt><dd>{date.format(appeal.closedAt)}</dd></div>{/if}
		{#if appeal.closerName}<div><dt>Decided by</dt><dd>{appeal.closerName}</dd></div>{/if}
	</dl>

	<section>
		<h2>Why it should change</h2>
		<p class="text">{appeal.reason}</p>
		<EvidenceGallery evidence={appeal.evidence} {refresh} label="Evidence attached to this appeal" />
	</section>

	<section aria-labelledby="decisions-title">
		<h2 id="decisions-title">Appealed decisions</h2>
		<ul class="decisions">
			{#each appeal.decisions as decision (decision.id)}
				{@const href = targetHref(decision.target)}
				<li data-outcome={decision.outcome ?? 'pending'}>
					<div class="decision-head">
						<strong>
							{#if href}<a {href}>{decisionKindLabels[decision.target.kind]}</a>{:else}{decisionKindLabels[
									decision.target.kind
								]}{/if}
						</strong>
						{#if decision.outcome}<span class="outcome">{decisionOutcomeLabels[decision.outcome]}</span>{/if}
					</div>
					{#if decision.counterpartId}
						<p class="secondary-text">
							Also affects {decision.counterpartId === callerId ? 'you' : decision.counterpartName || 'another user'}
						</p>
					{/if}
					{#if decision.outcomeReason}<p class="text">{decision.outcomeReason}</p>{/if}
				</li>
			{/each}
		</ul>
	</section>
</article>

<style>
	.summary { display: grid; gap: 1rem; }
	header { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 0.5rem 1rem; }
	h1 { margin: 0; }
	h2 { margin: 0 0 0.35rem; font-size: 1.1rem; }
	section { display: grid; gap: 0.5rem; }
	p { margin: 0; }
	.facts { display: flex; flex-wrap: wrap; gap: 0.35rem 2.5rem; margin: 0; }
	.facts dt { color: var(--m3c-on-surface-variant); font-size: 0.75rem; }
	.facts dd { margin: 0.1rem 0 0; }
	.text { white-space: pre-wrap; overflow-wrap: anywhere; }
	.decisions { display: grid; gap: 0.5rem; margin: 0; padding: 0; list-style: none; }
	.decisions li {
		display: grid;
		gap: 0.25rem;
		padding: 0.6rem 0.9rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
	}
	.decisions li[data-outcome='overturned'] { border-color: var(--m3c-primary); }
	.decision-head { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 0.5rem; }
	.outcome { font-size: 0.875rem; font-weight: 500; }
</style>
