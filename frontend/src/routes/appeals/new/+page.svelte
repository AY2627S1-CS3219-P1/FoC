<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, Checkbox, LoadingIndicator, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { appealApi, authService, reportApi, reviewApi, snackbarService, warningService } from '$lib/services';
	import { characterCount, describeModerationError } from '$lib/services/moderation';
	import { decisionKindLabels, type DecisionKind, type DecisionTarget } from '$lib/services/appeal/appealApi';
	import { submitAppealSchema } from '$lib/services/appeal/appealSchemas';
	import { appealableDecision, reportCategoryLabels, reportOutcomeLabels } from '$lib/services/report/reportApi';
	import EvidenceField from '$lib/components/moderation/EvidenceField.svelte';

	type Candidate = { key: string; target: DecisionTarget; title: string; detail: string };

	// The decisions a user may appeal: closed report outcomes and reviews their
	// reports kept, active warnings, and reviews removed outside a report. The
	// server makes the final eligibility check.
	const listSize = 100;
	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium' });
	const keyOf = (target: DecisionTarget) => `${target.kind}:${target.id}`;
	const preselectParams: [string, DecisionKind][] = [
		['reportId', 'report'],
		['warningId', 'warning'],
		['removedReviewId', 'removed_review'],
		['retainedReviewId', 'retained_review']
	];

	let candidates = $state<Candidate[]>([]);
	let loading = $state(true);
	let loadError = $state('');
	let reload = $state(0);
	let evidenceField: EvidenceField | undefined = $state();

	const callerId = $derived(authService.user?.id ?? '');
	const preselected = $derived(
		preselectParams.flatMap(([param, kind]) => {
			const id = page.url.searchParams.get(param);
			return id ? [{ kind, id }] : [];
		})
	);

	async function loadCandidates(caller: string, signal: AbortSignal): Promise<Candidate[]> {
		const [reports, warnings, removals] = await Promise.all([
			reportApi.listMyReports({ pageSize: listSize }),
			warningService.listMyWarnings({ pageSize: listSize }),
			reviewApi.listMyRemovals({ page: 1, pageSize: listSize }, signal)
		]);

		const found: Candidate[] = [];
		for (const report of reports.reports) {
			const decision = appealableDecision(report, caller);
			if (!decision || !report.outcome) continue;
			const target: DecisionTarget = { kind: decision.kind, id: decision.id };
			found.push({
				key: keyOf(target),
				target,
				title:
					decision.kind === 'retained_review'
						? `Kept review by ${report.subjectName || 'a user'}`
						: `${reportOutcomeLabels[report.outcome]} on a ${reportCategoryLabels[report.category].toLowerCase()} report`,
				detail: `Report closed ${report.closedAt ? date.format(report.closedAt) : ''} · ${report.outcomeReason}`
			});
		}
		for (const warning of warnings.warnings) {
			if (warning.status !== 'active') continue;
			const target: DecisionTarget = { kind: 'warning', id: warning.id };
			found.push({
				key: keyOf(target),
				target,
				title: 'Account warning',
				detail: `Issued ${date.format(warning.createdAt)} · ${warning.reason}`
			});
		}
		for (const removal of removals.removals) {
			const target: DecisionTarget = { kind: 'removed_review', id: removal.reviewId };
			found.push({
				key: keyOf(target),
				target,
				title: 'Your removed review',
				detail: `Removed ${date.format(new Date(removal.createdAt))} · ${removal.reason}`
			});
		}
		return found;
	}

	// One key per form: a retry after an ambiguous failure returns the appeal
	// that may already exist instead of filing a second one.
	const idempotencyKey = crypto.randomUUID();

	const formId = $props.id();
	const { form, errors, enhance, submitting, message } = superForm(
		defaults({ decisions: [], reason: '' }, zod4(submitAppealSchema)),
		{
			id: formId,
			SPA: true,
			dataType: 'json',
			validators: zod4Client(submitAppealSchema),
			validationMethod: 'onblur',
			multipleSubmits: 'prevent',
			resetForm: false,
			taintedMessage: true,
			async onUpdate({ form: validated }) {
				if (!validated.valid) return;
				const evidenceIds = evidenceField?.readyEvidenceIds();
				if (!evidenceIds) return;
				const chosen = new Set(validated.data.decisions);
				const decisions = candidates.filter((candidate) => chosen.has(candidate.key)).map((c) => c.target);
				try {
					const appeal = await appealApi.submitAppeal({
						idempotencyKey,
						reason: validated.data.reason,
						decisions,
						evidenceIds
					});
					snackbarService.show('Appeal submitted. An administrator will review it.', { severity: 'success' });
					await goto(`/appeals/${appeal.id}`, { replaceState: true });
				} catch (cause) {
					setMessage(validated, describeModerationError(cause, 'submit the appeal'));
				}
			}
		}
	);

	$effect(() => {
		const caller = callerId;
		const wanted = preselected;
		void reload;
		if (!caller) return;
		const controller = new AbortController();
		loading = true;
		loadError = '';
		loadCandidates(caller, controller.signal)
			.then((found) => {
				if (controller.signal.aborted) return;
				// A linked decision beyond the first page is still offered.
				for (const target of wanted) {
					if (!found.some((candidate) => candidate.key === keyOf(target))) {
						found.unshift({ key: keyOf(target), target, title: decisionKindLabels[target.kind], detail: '' });
					}
				}
				candidates = found;
				untrack(() => form.update((data) => ({ ...data, decisions: wanted.map(keyOf) }), { taint: false }));
			})
			.catch((cause) => {
				if (!controller.signal.aborted) loadError = describeModerationError(cause, 'load your decisions');
			})
			.finally(() => {
				if (!controller.signal.aborted) loading = false;
			});
		return () => controller.abort();
	});
</script>

<svelte:head><title>New appeal · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="new-appeal-title">
	<div><Button variant="text" href="/appeals">← Your appeals</Button></div>
	<header class="page-heading">
		<div>
			<p class="eyebrow">Appeals</p>
			<h1 id="new-appeal-title">Appeal a decision</h1>
			<p>
				An administrator reviews your appeal and decides whether each decision stands. If a decision is
				overturned, its effect is undone.
			</p>
		</div>
	</header>

	{#if loading}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading decisions" /><p>Loading decisions you can appeal…</p></div>
	{:else if loadError}
		<Card variant="outlined">
			<div class="panel-body">
				<p class="notice error" role="alert">{loadError}</p>
				<div class="form-actions"><Button onclick={() => reload++}>Try again</Button></div>
			</div>
		</Card>
	{:else if candidates.length === 0}
		<Card variant="outlined">
			<div class="panel-body"><p>You have no decisions to appeal.</p></div>
		</Card>
	{:else}
		<Card variant="outlined">
			<form class="panel-body appeal-form" method="POST" use:enhance>
				<fieldset aria-describedby={`${formId}-decisions-error`}>
					<legend>Decisions to appeal</legend>
					{#each candidates as candidate (candidate.key)}
						<label class="decision">
							<Checkbox>
								<input type="checkbox" name="decisions" value={candidate.key} bind:group={$form.decisions} />
							</Checkbox>
							<span>
								<strong>{candidate.title}</strong>
								{#if candidate.detail}<span class="secondary-text">{candidate.detail}</span>{/if}
							</span>
						</label>
					{/each}
					{#if $errors.decisions?._errors}
						<small class="field-error" id={`${formId}-decisions-error`}>{$errors.decisions._errors[0]}</small>
					{/if}
				</fieldset>
				<div class="field">
					<TextFieldOutlinedMultiline
						label="Why should these decisions change?"
						name="reason"
						rows={6}
						bind:value={$form.reason}
						error={!!$errors.reason}
						aria-invalid={$errors.reason ? 'true' : undefined}
						aria-describedby={`${formId}-reason-meta`}
					/>
					<small class="field-meta" id={`${formId}-reason-meta`}>
						{#if $errors.reason}<span class="field-error">{$errors.reason[0]}</span>{/if}
						<span>{characterCount($form.reason).toLocaleString('en-SG')}/4,000</span>
					</small>
				</div>
				<EvidenceField
					bind:this={evidenceField}
					upload={(input, signal) => appealApi.createEvidenceUpload(input, signal)}
					disabled={$submitting}
				/>
				{#if $message}<p class="notice error" role="alert">{$message}</p>{/if}
				<div class="form-actions">
					<Button variant="text" href="/appeals">Cancel</Button>
					<Button type="submit" disabled={$submitting}>{$submitting ? 'Submitting…' : 'Submit appeal'}</Button>
				</div>
			</form>
		</Card>
	{/if}
</section>

<style>
	.appeal-form { display: grid; gap: 1rem; }
	fieldset { display: grid; gap: 0.5rem; margin: 0; padding: 0; border: 0; min-width: 0; }
	legend { margin-bottom: 0.5rem; font-weight: 500; }
	.decision {
		display: flex;
		align-items: flex-start;
		gap: 0.75rem;
		padding: 0.6rem 0.75rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
		cursor: pointer;
	}
	.decision > span { display: grid; gap: 0.15rem; min-width: 0; overflow-wrap: anywhere; }
	.field { display: grid; gap: 0.25rem; }
	.field-meta { display: flex; justify-content: space-between; gap: 1rem; }
</style>
