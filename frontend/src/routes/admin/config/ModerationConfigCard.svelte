<script lang="ts">
	import { Button, Card, LoadingIndicator, SelectOutlined, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { ModerationOutputMode } from '$lib/gen/moderation/v1/config_pb';
	import {
		moderationConfigService,
		snackbarService
	} from '$lib/services';
	import {
		buildModerationParameters,
		ParameterValidationError,
		type ModerationParameterInput
	} from '$lib/services/moderation-config/parameters';
	import { describeModerationConfigError, extractValidationMessages } from '$lib/services/moderation-config/errors';

	// CMS-owned configuration card: model selection plus supported inference
	// parameters, with the draft save → test → activate flow. Values live in
	// immutable versions server-side; this card never edits the active
	// configuration in place. Credentials never appear here.
	let modelId = $state('');
	let temperatureText = $state('');
	let topPText = $state('');
	let presencePenaltyText = $state('');
	let frequencyPenaltyText = $state('');
	let budgetKind = $state<'none' | 'max_completion_tokens' | 'max_tokens'>('none');
	let budgetText = $state('');
	let stopText = $state('');
	let seedText = $state('');
	let reasoningEffort = $state('');
	let advancedText = $state('');
	let outputMode = $state(String(ModerationOutputMode.JSON_SCHEMA));
	let draftVersionId = $state('');
	let sampleComment = $state(
		'The courier arrived on time and handled the parcel carefully. Highly recommended.'
	);
	let formError = $state('');

	const isLoading = $derived(moderationConfigService.isBusy);
	const setup = $derived(moderationConfigService.setup);
	const models = $derived(moderationConfigService.models);
	const active = $derived(moderationConfigService.activeConfiguration);
	const testResult = $derived(moderationConfigService.testResult);

	function numberOrNull(text: string): number | undefined {
		const trimmed = text.trim();
		if (trimmed === '') return undefined;
		const value = Number(trimmed);
		return Number.isFinite(value) ? value : undefined;
	}

	function currentInput(): ModerationParameterInput {
		const budget = budgetText.trim() === '' ? undefined : Number(budgetText.trim());
		return {
			temperature: numberOrNull(temperatureText),
			topP: numberOrNull(topPText),
			presencePenalty: numberOrNull(presencePenaltyText),
			frequencyPenalty: numberOrNull(frequencyPenaltyText),
			maxCompletionTokens: budgetKind === 'max_completion_tokens' && budget !== undefined ? budget : undefined,
			maxTokens: budgetKind === 'max_tokens' && budget !== undefined ? budget : undefined,
			seed: seedText.trim() === '' ? undefined : Number(seedText.trim()),
			stop: stopText.trim() === '' ? undefined : stopText.split(','),
			reasoningEffort: reasoningEffort.trim() === '' ? undefined : reasoningEffort.trim(),
			advancedParameters: parseAdvanced(advancedText)
		};
	}

	function parseAdvanced(text: string): { [key: string]: string } | undefined {
		const trimmed = text.trim();
		if (trimmed === '') return undefined;
		const parsed: { [key: string]: string } = {};
		for (const pair of trimmed.split('\n')) {
			const line = pair.trim();
			if (line === '') continue;
			const index = line.indexOf('=');
			if (index <= 0) throw new ParameterValidationError('advancedParameters', `Advanced parameter "${line}" must look like name=value.`);
			parsed[line.slice(0, index).trim()] = line.slice(index + 1).trim();
		}
		return parsed;
	}

	async function loadAll(): Promise<void> {
		try {
			await moderationConfigService.loadAll();
			if (moderationConfigService.activeConfiguration) {
				modelId = moderationConfigService.activeConfiguration.modelId;
				outputMode = String(moderationConfigService.activeConfiguration.outputMode);
			}
		} catch {
			formError = describeModerationConfigError(moderationConfigService.error);
		}
	}

	async function saveDraft(): Promise<void> {
		formError = '';
		let parameters;
		try {
			parameters = buildModerationParameters(currentInput());
		} catch (cause) {
			formError = cause instanceof ParameterValidationError ? cause.message : describeModerationConfigError(cause);
			return;
		}
		if (modelId.trim() === '') {
			formError = 'Choose a model before saving a draft.';
			return;
		}
		try {
			draftVersionId = await moderationConfigService.saveDraft(
				modelId.trim(),
				parameters,
				Number(outputMode) as ModerationOutputMode
			);
			snackbarService.show('Draft saved. Test it before activating.', { severity: 'success' });
		} catch {
			formError = describeModerationConfigError(moderationConfigService.error);
		}
	}

	async function testDraft(): Promise<void> {
		formError = '';
		if (draftVersionId === '') {
			formError = 'Save a draft first, then test it.';
			return;
		}
		if (sampleComment.trim() === '') {
			formError = 'Provide a short sample comment for the test.';
			return;
		}
		try {
			await moderationConfigService.testDraft(draftVersionId, sampleComment.trim());
		} catch {
			formError = describeModerationConfigError(moderationConfigService.error);
		}
	}

	async function activateDraft(): Promise<void> {
		formError = '';
		if (draftVersionId === '') {
			formError = 'Save and test a draft before activating it.';
			return;
		}
		try {
			await moderationConfigService.activateDraft(draftVersionId, active?.versionId ?? '');
			draftVersionId = '';
			snackbarService.show('Configuration activated.', { severity: 'success' });
		} catch {
			formError = describeModerationConfigError(moderationConfigService.error);
		}
	}

	function validationMessages(): string[] {
		return extractValidationMessages(moderationConfigService.error);
	}

	function describeTestOutcome(): string {
		const result = testResult;
		if (!result) return '';
		if (result.errorClass !== '') return `Test failed: ${result.errorClass}.`;
		return `Decision: ${result.decision}${result.reasonCodes.length ? ` (${result.reasonCodes.join(', ')})` : ''} · ${result.durationMs}ms`;
	}

	$effect(() => {
		void loadAll();
	});
</script>

<Card variant="outlined">
	<div class="panel-body">
		<div><p class="eyebrow">Content moderation</p><h2 class="card-title">Model configuration</h2></div>

		{#if isLoading && !setup}
			<div class="loading-state" role="status" aria-live="polite">
				<LoadingIndicator aria-label="Loading moderation configuration" />
				<p>Loading moderation configuration…</p>
			</div>
		{:else if setup}
			{#if !setup.hasProviderCredential}
				<p class="notice" role="status">
					No server-side model credential is configured. You can still save drafts, but tests and
					worker assessments will fail until an operator sets the credential.
				</p>
			{:else if !setup.hasActiveConfiguration}
				<p class="notice" role="status">
					No model is active yet. New reviews queue without consuming attempts until a
					configuration is activated.
				</p>
			{/if}

			<dl class="config-facts">
				<div><dt>Active version</dt><dd>{active ? active.versionId : '— none —'}</dd></div>
				<div><dt>Model</dt><dd>{active ? active.modelId : '—'}</dd></div>
				<div><dt>Policy</dt><dd>{setup.policyVersion}</dd></div>
			</dl>

			<form class="config-form" onsubmit={(event) => event.preventDefault()}>
				<div class="field">
					<SelectOutlined
						label="Model"
						bind:value={modelId}
						options={models.map((model) => ({ value: model.modelId, text: model.modelId }))}
					/>
					<div class="form-actions">
						<Button type="button" variant="text" disabled={moderationConfigService.isLoading('models')} onclick={() => moderationConfigService.loadModels()}>
							{moderationConfigService.isLoading('models') ? 'Discovering…' : 'Refresh model list'}
						</Button>
					</div>
				</div>

				<div class="field-grid">
					<TextFieldOutlined label="Temperature" name="temperature" bind:value={temperatureText} placeholder="provider default" />
					<TextFieldOutlined label="top_p" name="topP" bind:value={topPText} placeholder="provider default" />
					<TextFieldOutlined label="Presence penalty" name="presencePenalty" bind:value={presencePenaltyText} placeholder="provider default" />
					<TextFieldOutlined label="Frequency penalty" name="frequencyPenalty" bind:value={frequencyPenaltyText} placeholder="provider default" />
				</div>

				<div class="field-grid">
					<div class="field">
						<SelectOutlined
							label="Token budget field"
							bind:value={budgetKind}
							options={[
								{ value: 'none', text: 'Not set' },
								{ value: 'max_completion_tokens', text: 'max_completion_tokens' },
								{ value: 'max_tokens', text: 'max_tokens' }
							]}
						/>
					</div>
					{#if budgetKind !== 'none'}
						<TextFieldOutlined label="Token budget" name="budget" bind:value={budgetText} />
					{/if}
					<TextFieldOutlined label="Seed" name="seed" bind:value={seedText} placeholder="provider default" />
					<TextFieldOutlined label="Reasoning effort" name="reasoningEffort" bind:value={reasoningEffort} placeholder="provider default" />
				</div>

				<TextFieldOutlined label="Stop sequences (comma-separated)" name="stop" bind:value={stopText} placeholder="provider default" />

				<div class="field">
					<SelectOutlined
						label="Output mode"
						bind:value={outputMode}
						options={[
							{ value: String(ModerationOutputMode.JSON_SCHEMA), text: 'Strict JSON schema (preferred)' },
							{ value: String(ModerationOutputMode.JSON_OBJECT), text: 'JSON mode (locally validated)' }
						]}
					/>
					<small class="field-meta">
						Both modes are validated locally; strict schema is only used when the model supports it.
					</small>
				</div>

				<TextFieldOutlinedMultiline
					label="Advanced parameters (name=value per line, allowlisted)"
					name="advanced"
					rows={3}
					bind:value={advancedText}
					placeholder={"top_k=40\ndialog_examples=true"}
				/>

				<div class="field-actions">
					<Button type="button" disabled={moderationConfigService.isLoading('save')} onclick={() => void saveDraft()}>
						{moderationConfigService.isLoading('save') ? 'Saving…' : 'Save draft'}
					</Button>
					{#if draftVersionId}
						<span class="draft-chip">Draft {draftVersionId.slice(0, 8)}…</span>
					{/if}
				</div>

				<div class="field">
					<TextFieldOutlinedMultiline label="Sample comment for test" name="sample" rows={2} bind:value={sampleComment} />
					<div class="field-actions">
						<Button type="button" variant="outlined" disabled={moderationConfigService.isLoading('test') || draftVersionId === ''} onclick={() => void testDraft()}>
							{moderationConfigService.isLoading('test') ? 'Testing…' : 'Test draft'}
						</Button>
						<Button type="button" disabled={moderationConfigService.isLoading('activate') || draftVersionId === ''} onclick={() => void activateDraft()}>
							{moderationConfigService.isLoading('activate') ? 'Activating…' : 'Activate draft'}
						</Button>
					</div>
					{#if testResult}<p class="notice" role="status">{describeTestOutcome()}</p>{/if}
				</div>

				{#if formError}
					<p class="notice error" role="alert">{formError}</p>
					{#each validationMessages() as message (message)}
						<p class="notice error" role="alert">{message}</p>
					{/each}
				{/if}
			</form>
		{/if}
	</div>
</Card>
