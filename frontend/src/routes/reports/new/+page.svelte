<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, SelectOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { reportApi, snackbarService } from '$lib/services';
	import { characterCount, describeModerationError } from '$lib/services/moderation';
	import {
		REPORT_CATEGORIES,
		reportCategoryLabels,
		type SubmitReportTarget
	} from '$lib/services/report/reportApi';
	import { submitReportSchema } from '$lib/services/report/reportSchemas';
	import EvidenceField from '$lib/components/moderation/EvidenceField.svelte';

	// A report targets exactly one review (`?reviewId=`) or one order and the
	// other participant on it (`?orderId=&subjectId=`).
	const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
	const target = $derived.by((): SubmitReportTarget | null => {
		const params = page.url.searchParams;
		const reviewId = params.get('reviewId') ?? '';
		const orderId = params.get('orderId') ?? '';
		const subjectId = params.get('subjectId') ?? '';
		if (uuid.test(reviewId)) return { kind: 'review', reviewId };
		if (uuid.test(orderId) && uuid.test(subjectId)) return { kind: 'order', orderId, subjectId };
		return null;
	});

	let evidenceField: EvidenceField | undefined = $state();

	const formId = $props.id();
	const { form, errors, enhance, submitting, message } = superForm(
		defaults({ category: undefined, description: '' }, zod4(submitReportSchema)),
		{
			id: formId,
			SPA: true,
			validators: zod4Client(submitReportSchema),
			validationMethod: 'onblur',
			multipleSubmits: 'prevent',
			resetForm: false,
			taintedMessage: true,
			async onUpdate({ form: validated }) {
				if (!validated.valid || !target) return;
				const evidenceIds = evidenceField?.readyEvidenceIds();
				if (!evidenceIds) return;
				try {
					const report = await reportApi.submitReport({
						target,
						category: validated.data.category,
						description: validated.data.description,
						evidenceIds
					});
					snackbarService.show('Report submitted. An administrator will review it.', { severity: 'success' });
					await goto(`/reports/${report.id}`, { replaceState: true });
				} catch (cause) {
					evidenceField?.rejectFrom(cause);
					setMessage(validated, describeModerationError(cause, 'submit the report'));
				}
			}
		}
	);
</script>

<svelte:head><title>Report a problem · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="new-report-title">
	<div><Button variant="text" href="/reports">← Your reports</Button></div>
	<header class="page-heading">
		<div>
			<p class="eyebrow">Reports</p>
			<h1 id="new-report-title">{target?.kind === 'review' ? 'Report a review' : 'Report an order'}</h1>
			<p>
				An administrator reviews every report and may ask the other person for their side. Both of you can see
				the report and its outcome.
			</p>
		</div>
	</header>

	{#if !target}
		<Card variant="outlined">
			<div class="panel-body">
				<p class="notice error" role="alert">
					Start a report from the order or review you want to report.
				</p>
			</div>
		</Card>
	{:else}
		<Card variant="outlined">
			<form class="panel-body report-form" method="POST" use:enhance>
				{#if target.kind === 'order'}
					<p>
						You are reporting the other participant on
						<a href={`/orders/${encodeURIComponent(target.orderId)}`}>this order</a>.
					</p>
				{/if}
				<div class="field">
					<SelectOutlined
						label="Category"
						name="category"
						bind:value={$form.category}
						options={REPORT_CATEGORIES.map((category) => ({
							value: category,
							text: reportCategoryLabels[category]
						}))}
					/>
					{#if $errors.category}<small class="field-error">{$errors.category[0]}</small>{/if}
				</div>
				<div class="field">
					<TextFieldOutlinedMultiline
						label="What happened"
						name="description"
						rows={6}
						bind:value={$form.description}
						error={!!$errors.description}
						aria-invalid={$errors.description ? 'true' : undefined}
						aria-describedby={`${formId}-description-meta`}
					/>
					<small class="field-meta" id={`${formId}-description-meta`}>
						{#if $errors.description}<span class="field-error">{$errors.description[0]}</span>{/if}
						<span>{characterCount($form.description).toLocaleString('en-SG')}/4,000</span>
					</small>
				</div>
				<EvidenceField
					bind:this={evidenceField}
					upload={(input, signal) => reportApi.createEvidenceUpload(input, signal)}
					disabled={$submitting}
				/>
				{#if $message}<p class="notice error" role="alert">{$message}</p>{/if}
				<div class="form-actions">
					<Button variant="text" href="/reports">Cancel</Button>
					<Button type="submit" disabled={$submitting}>{$submitting ? 'Submitting…' : 'Submit report'}</Button>
				</div>
			</form>
		</Card>
	{/if}
</section>

<style>
	.report-form { display: grid; gap: 1rem; }
	.report-form p { margin: 0; }
	.field { display: grid; gap: 0.25rem; }
	.field-meta { display: flex; justify-content: space-between; gap: 1rem; }
</style>
