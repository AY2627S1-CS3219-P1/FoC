<script lang="ts">
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { appealApi, authService } from '$lib/services';
	import { describeModerationError } from '$lib/services/moderation';
	import type { AppealDetail } from '$lib/services/appeal/appealApi';
	import AppealSummary from '$lib/components/moderation/AppealSummary.svelte';
	import ResponseThread from '$lib/components/moderation/ResponseThread.svelte';
	import ResponseForm from '$lib/components/moderation/ResponseForm.svelte';

	let detail = $state<AppealDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);

	const callerId = $derived(authService.user?.id ?? '');
	const appeal = $derived(detail?.appeal ?? null);
	// Only the participant an administrator asked may reply, while awaited.
	const awaited = $derived(
		!!appeal && appeal.status === 'awaiting_response' && !!callerId && appeal.awaitedUserId === callerId
	);

	const refresh = (id: string) => appealApi.getEvidenceDownload(id);

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

	async function respond(body: string, evidenceIds: string[]) {
		if (!appeal) return;
		await appealApi.submitResponse(appeal.id, body, evidenceIds);
		reload++;
	}
</script>

<svelte:head><title>Appeal · Friend on Campus</title></svelte:head>

<section class="page-content">
	<div><Button variant="text" href="/appeals">← Your appeals</Button></div>

	{#if loading && !detail}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading appeal" /><p>Loading appeal…</p></div>
	{:else if error}
		<Card variant="outlined">
			<div class="panel-body">
				<p class="notice error" role="alert">{error}</p>
				<div class="form-actions"><Button onclick={() => reload++}>Try again</Button></div>
			</div>
		</Card>
	{:else if detail && appeal}
		{#if awaited}
			<p class="notice" role="status">An administrator has asked for your response to this appeal.</p>
		{/if}
		<Card variant="outlined">
			<div class="panel-body"><AppealSummary {appeal} {refresh} {callerId} /></div>
		</Card>
		<Card variant="outlined">
			<div class="panel-body"><ResponseThread responses={detail.responses} {refresh} {callerId} /></div>
		</Card>
		{#if awaited}
			<Card variant="outlined">
				<div class="panel-body">
					<ResponseForm
						heading="Respond to this appeal"
						help="Your response is shared with the appellant and the administrators reviewing it."
						upload={(input, signal) => appealApi.createEvidenceUpload(input, signal)}
						onsubmit={respond}
					/>
				</div>
			</Card>
		{/if}
	{/if}
</section>
