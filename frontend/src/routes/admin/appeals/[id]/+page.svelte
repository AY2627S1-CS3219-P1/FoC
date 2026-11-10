<script lang="ts">
	import { untrack } from 'svelte';
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator, SelectOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { appealApi, authService, snackbarService } from '$lib/services';
	import { characterCount, describeModerationError } from '$lib/services/moderation';
	import {
		counterparts,
		decisionKindLabels,
		isAppealParticipant,
		type AppealDetail,
		type DecisionResolution
	} from '$lib/services/appeal/appealApi';
	import { closeAppealSchema } from '$lib/services/appeal/appealSchemas';
	import AppealSummary from '$lib/components/moderation/AppealSummary.svelte';
	import ResponseThread from '$lib/components/moderation/ResponseThread.svelte';
	import ConfirmDialog from '$lib/components/admin/ConfirmDialog.svelte';

	let detail = $state<AppealDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);
	let busy = $state(false);
	let confirmOpen = $state(false);
	let pendingResolutions = $state<DecisionResolution[] | null>(null);
	let participantId = $state('');

	const callerId = $derived(authService.user?.id ?? '');
	const appeal = $derived(detail?.appeal ?? null);
	// The server refuses an administrator who filed the appeal or is affected by it.
	const involved = $derived(!!appeal && isAppealParticipant(appeal, callerId));
	const open = $derived(!!appeal && appeal.status !== 'closed');
	const responders = $derived(appeal ? counterparts(appeal) : []);
	const awaitedName = $derived(
		responders.find((responder) => responder.id === appeal?.awaitedUserId)?.name || 'the participant'
	);
	const overturned = $derived(pendingResolutions?.filter((resolution) => resolution.outcome === 'overturned') ?? []);

	const refresh = (id: string) => appealApi.getEvidenceDownload(id);
	const reportHref = (id: string) => `/admin/reports/${encodeURIComponent(id)}`;

	$effect(() => {
		const id = page.params.id ?? '';
		void reload;
		let stale = false;
		loading = true;
		error = '';
		appealApi
			.getAppeal(id)
			.then((value) => {
				if (!stale) detail = value;
			})
			.catch((cause) => {
				if (!stale) {
					detail = null;
					error = describeModerationError(cause, 'load this appeal');
				}
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});

	$effect(() => {
		if (!responders.some((responder) => responder.id === untrack(() => participantId))) {
			participantId = responders[0]?.id ?? '';
		}
	});

	async function act(label: string, action: (id: string) => Promise<unknown>, done: string): Promise<boolean> {
		if (!appeal || busy) return false;
		const id = appeal.id;
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
	const closeForm = superForm(defaults({ resolutions: [] }, zod4(closeAppealSchema)), {
		id: formId,
		SPA: true,
		dataType: 'json',
		validators: zod4Client(closeAppealSchema),
		validationMethod: 'onblur',
		multipleSubmits: 'prevent',
		resetForm: false,
		taintedMessage: true,
		async onUpdate({ form: validated }) {
			if (!validated.valid) return;
			pendingResolutions = validated.data.resolutions;
			confirmOpen = true;
		}
	});
	const { form, errors, enhance } = closeForm;

	// One resolution row per decision, kept when the same decisions reload.
	$effect(() => {
		const ids = appeal?.decisions.map((decision) => decision.id) ?? [];
		untrack(() =>
			form.update(
				(data) => ({
					resolutions: ids.map(
						(decisionId) =>
							data.resolutions.find((resolution) => resolution.decisionId === decisionId) ?? {
								decisionId,
								outcome: 'upheld' as const,
								reason: ''
							}
					)
				}),
				{ taint: false }
			)
		);
	});

	async function confirmClose() {
		const resolutions = pendingResolutions;
		if (!resolutions) return;
		const closed = await act('close the appeal', (id) => appealApi.closeAppeal(id, resolutions), 'Appeal closed.');
		confirmOpen = false;
		pendingResolutions = null;
		if (closed) closeForm.reset();
	}

	function decisionLabel(decisionId: string): string {
		const decision = appeal?.decisions.find((candidate) => candidate.id === decisionId);
		return decision ? decisionKindLabels[decision.target.kind] : 'Decision';
	}
</script>

<svelte:head><title>Review appeal · Friend on Campus</title></svelte:head>
<section class="page-content admin-content">
	<div><Button variant="text" href="/admin/appeals">← Appeals</Button></div>

	{#if loading && !detail}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading appeal" /><p>Loading appeal…</p></div>
	{:else if error}
		<p class="notice error" role="alert">{error}</p>
		<div><Button onclick={() => reload++}>Try again</Button></div>
	{:else if detail && appeal}
		<Card variant="outlined">
			<div class="panel-body"><AppealSummary {appeal} {refresh} {callerId} {reportHref} /></div>
		</Card>
		<Card variant="outlined">
			<div class="panel-body"><ResponseThread responses={detail.responses} {refresh} {callerId} /></div>
		</Card>

		{#if open && involved}
			<p class="notice" role="status">You filed this appeal or it affects you, so another administrator must decide it.</p>
		{:else if open}
			<Card variant="outlined">
				<div class="panel-body moderation">
					<h2>Moderate</h2>
					{#if appeal.status === 'awaiting_response'}
						<p class="secondary-text">Waiting for {awaitedName} to respond.</p>
					{/if}
					{#if appeal.status === 'open'}
						<div>
							<Button
								variant="tonal"
								disabled={busy}
								onclick={() => act('begin the review', (id) => appealApi.beginReview(id), 'Review started.')}
							>
								Begin review
							</Button>
						</div>
					{/if}
					{#if appeal.status === 'under_review' && responders.length > 0}
						<div class="request">
							<SelectOutlined
								label="Ask to respond"
								bind:value={participantId}
								options={responders.map((responder) => ({ value: responder.id, text: responder.name || 'A participant' }))}
							/>
							<Button
								variant="tonal"
								disabled={busy || !participantId}
								onclick={() =>
									act(
										'request a response',
										(id) => appealApi.requestResponse(id, participantId),
										'Response requested.'
									)}
							>
								Request response
							</Button>
						</div>
					{/if}

					<form class="close-form" method="POST" use:enhance aria-labelledby={`${formId}-title`}>
						<h3 id={`${formId}-title`}>Close with a decision on each item</h3>
						<p class="secondary-text">
							Overturned decisions are undone when you close the appeal. Each reason is shown to the participants.
						</p>
						{#each $form.resolutions as resolution, index (resolution.decisionId)}
							<fieldset class="resolution">
								<legend>{decisionLabel(resolution.decisionId)}</legend>
								<SelectOutlined
									label="Outcome"
									bind:value={$form.resolutions[index].outcome}
									options={[
										{ value: 'upheld', text: 'Upheld: the decision stands' },
										{ value: 'overturned', text: 'Overturned: undo the decision' }
									]}
								/>
								<TextFieldOutlinedMultiline
									label="Reason"
									rows={3}
									bind:value={$form.resolutions[index].reason}
									error={!!$errors.resolutions?.[index]?.reason}
									aria-invalid={$errors.resolutions?.[index]?.reason ? 'true' : undefined}
								/>
								<small class="field-meta">
									{#if $errors.resolutions?.[index]?.reason}
										<span class="field-error">{$errors.resolutions[index].reason[0]}</span>
									{/if}
									<span>{characterCount(resolution.reason).toLocaleString('en-SG')}/2,000</span>
								</small>
							</fieldset>
						{/each}
						<div class="form-actions"><Button type="submit" disabled={busy}>Close appeal</Button></div>
					</form>
				</div>
			</Card>
		{/if}
	{/if}
</section>

<ConfirmDialog bind:open={confirmOpen} title="Close this appeal?" label="Close appeal" {busy} onconfirm={confirmClose}>
	{#if overturned.length > 0}
		<p>These decisions will be overturned and undone:</p>
		<ul>
			{#each overturned as resolution (resolution.decisionId)}<li>{decisionLabel(resolution.decisionId)}</li>{/each}
		</ul>
	{:else}
		<p>Every decision will stand.</p>
	{/if}
	<p>Closing is final.</p>
</ConfirmDialog>

<style>
	.moderation { display: grid; gap: 1rem; }
	.moderation h2, .close-form h3 { margin: 0; }
	.moderation p { margin: 0; }
	.request { display: flex; flex-wrap: wrap; align-items: center; gap: 0.75rem; }
	.close-form { display: grid; gap: 0.75rem; padding-top: 1rem; border-top: 1px solid var(--m3c-outline-variant); }
	.resolution {
		display: grid;
		gap: 0.5rem;
		margin: 0;
		padding: 0.75rem 1rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
		min-width: 0;
	}
	legend { padding: 0 0.25rem; font-weight: 500; }
	.field-meta { display: flex; justify-content: space-between; gap: 1rem; }
</style>
