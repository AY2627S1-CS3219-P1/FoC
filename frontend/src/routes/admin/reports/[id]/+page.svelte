<script lang="ts">
	import { untrack } from 'svelte';
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator, SelectOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { authService, reportApi, snackbarService } from '$lib/services';
	import { characterCount, describeModerationError } from '$lib/services/moderation';
	import {
		REPORT_OUTCOMES,
		reportOutcomeLabels,
		type ReportDetail,
		type ReportOutcome
	} from '$lib/services/report/reportApi';
	import { closeReportSchema } from '$lib/services/report/reportSchemas';
	import ReportSummary from '$lib/components/moderation/ReportSummary.svelte';
	import ResponseThread from '$lib/components/moderation/ResponseThread.svelte';
	import ConfirmDialog from '$lib/components/admin/ConfirmDialog.svelte';

	let detail = $state<ReportDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);
	let busy = $state(false);
	let confirmOpen = $state(false);
	let pendingClose = $state<{ outcome: ReportOutcome; reason: string } | null>(null);

	const callerId = $derived(authService.user?.id ?? '');
	const report = $derived(detail?.report ?? null);
	// The server refuses an administrator who filed the report or is its subject.
	const involved = $derived(!!report && (report.reporterId === callerId || report.subjectId === callerId));
	const open = $derived(!!report && report.status !== 'closed');
	const outcomeOptions = $derived(
		REPORT_OUTCOMES.filter((outcome) => outcome !== 'review_removal' || report?.target.kind === 'review').map(
			(outcome) => ({ value: outcome, text: reportOutcomeLabels[outcome] })
		)
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

	async function act(label: string, action: (id: string) => Promise<unknown>, done: string): Promise<boolean> {
		if (!report || busy) return false;
		const id = report.id;
		busy = true;
		try {
			await action(id);
			snackbarService.show(done, { severity: 'success' });
			return true;
		} catch (cause) {
			snackbarService.show(describeModerationError(cause, label), { severity: 'error', timeout: 8000 });
			return false;
		} finally {
			busy = false;
			// Re-read after any result: a failure may follow a committed change.
			if (page.params.id === id) reload++;
		}
	}

	const formId = $props.id();
	const closeForm = superForm(
		defaults({ outcome: undefined, reason: '', reviewTarget: false }, zod4(closeReportSchema)),
		{
			id: formId,
			SPA: true,
			validators: zod4Client(closeReportSchema),
			validationMethod: 'onblur',
			multipleSubmits: 'prevent',
			resetForm: false,
			taintedMessage: true,
			async onUpdate({ form: validated }) {
				if (!validated.valid) return;
				pendingClose = { outcome: validated.data.outcome, reason: validated.data.reason };
				confirmOpen = true;
			}
		}
	);
	const { form, errors, enhance } = closeForm;

	// The schema allows removing the review only for a review report.
	$effect(() => {
		const reviewTarget = report?.target.kind === 'review';
		untrack(() => form.update((data) => ({ ...data, reviewTarget }), { taint: false }));
	});

	async function confirmClose() {
		const close = pendingClose;
		if (!close) return;
		const closed = await act(
			'close the report',
			(id) => reportApi.closeReport(id, close.outcome, close.reason),
			'Report closed.'
		);
		confirmOpen = false;
		pendingClose = null;
		if (closed) closeForm.reset();
	}
</script>

<svelte:head><title>Review report · Friend on Campus</title></svelte:head>
<section class="page-content admin-content">
	<div><Button variant="text" href="/admin/reports">← Reports</Button></div>

	{#if loading && !detail}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading report" /><p>Loading report…</p></div>
	{:else if error}
		<p class="notice error" role="alert">{error}</p>
		<div><Button onclick={() => reload++}>Try again</Button></div>
	{:else if detail && report}
		<Card variant="outlined">
			<div class="panel-body"><ReportSummary {report} {refresh} {callerId} /></div>
		</Card>
		<Card variant="outlined">
			<div class="panel-body"><ResponseThread responses={detail.responses} {refresh} {callerId} /></div>
		</Card>

		{#if open && involved}
			<p class="notice" role="status">You filed this report or it is about you, so another administrator must decide it.</p>
		{:else if open}
			<Card variant="outlined">
				<div class="panel-body moderation">
					<h2>Moderate</h2>
					{#if report.status === 'awaiting_response'}
						<p class="secondary-text">Waiting for {report.subjectName || 'the reported user'} to respond.</p>
					{/if}
					<div class="steps">
						{#if report.status === 'open'}
							<Button
								variant="tonal"
								disabled={busy}
								onclick={() => act('begin the review', (id) => reportApi.beginReview(id), 'Review started.')}
							>
								Begin review
							</Button>
						{/if}
						{#if report.status === 'under_review'}
							<Button
								variant="tonal"
								disabled={busy}
								onclick={() =>
									act('request a response', (id) => reportApi.requestResponse(id), 'Response requested.')}
							>
								Ask {report.subjectName || 'the reported user'} to respond
							</Button>
						{/if}
					</div>

					<form class="close-form" method="POST" use:enhance aria-labelledby={`${formId}-title`}>
						<h3 id={`${formId}-title`}>Close with an outcome</h3>
						<p class="secondary-text">
							The outcome is applied when you close the report. Both participants see the reason, and it is recorded on
							any warning or suspension.
						</p>
						<div class="field">
							<SelectOutlined label="Outcome" name="outcome" bind:value={$form.outcome} options={outcomeOptions} />
							{#if $errors.outcome}<small class="field-error">{$errors.outcome[0]}</small>{/if}
						</div>
						<div class="field">
							<TextFieldOutlinedMultiline
								label="Reason"
								name="reason"
								rows={4}
								bind:value={$form.reason}
								error={!!$errors.reason}
								aria-invalid={$errors.reason ? 'true' : undefined}
								aria-describedby={`${formId}-reason-meta`}
							/>
							<small class="field-meta" id={`${formId}-reason-meta`}>
								{#if $errors.reason}<span class="field-error">{$errors.reason[0]}</span>{/if}
								<span>{characterCount($form.reason).toLocaleString('en-SG')}/2,000</span>
							</small>
						</div>
						<div class="form-actions"><Button type="submit" disabled={busy}>Close report</Button></div>
					</form>
				</div>
			</Card>
		{/if}
	{/if}
</section>

<ConfirmDialog bind:open={confirmOpen} title="Close this report?" label="Close report" {busy} onconfirm={confirmClose}>
	{#if pendingClose}
		<p>
			Outcome: <strong>{reportOutcomeLabels[pendingClose.outcome]}</strong>.
			{#if pendingClose.outcome === 'suspension'}The reported user's account will be suspended.{/if}
			{#if pendingClose.outcome === 'warning'}A warning will be added to the reported user's account.{/if}
			{#if pendingClose.outcome === 'review_removal'}The reported review will be removed.{/if}
			This cannot be undone except through an appeal.
		</p>
	{/if}
</ConfirmDialog>

<style>
	.moderation { display: grid; gap: 1rem; }
	.moderation h2, .close-form h3 { margin: 0; }
	.moderation p { margin: 0; }
	.steps { display: flex; flex-wrap: wrap; gap: 0.75rem; }
	.steps:empty { display: none; }
	.close-form { display: grid; gap: 0.75rem; padding-top: 1rem; border-top: 1px solid var(--m3c-outline-variant); }
	.field { display: grid; gap: 0.25rem; }
	.field-meta { display: flex; justify-content: space-between; gap: 1rem; }
</style>
