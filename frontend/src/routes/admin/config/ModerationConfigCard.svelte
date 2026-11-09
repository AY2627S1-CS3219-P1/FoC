<script lang="ts">
	import { onMount } from 'svelte';
	import { Button, Card, Checkbox, LoadingIndicator, SelectOutlined, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { moderationConfigFormSchema, hydrateModerationConfig, moderationParametersFromForm } from './moderationConfigForm';
	import { ModerationOutputMode } from '$lib/gen/moderation/v1/config_pb';
	import {
		moderationConfigService,
		snackbarService
	} from '$lib/services';
	import {
		ParameterValidationError
	} from '$lib/services/moderation-config/parameters';
	import { describeModerationConfigError, extractValidationMessages } from '$lib/services/moderation-config/errors';

	// CMS-owned configuration card: model selection plus supported inference
	// parameters, with the draft save → test → activate flow. Values live in
	// immutable versions server-side; this card never edits the active
	// configuration in place. Credentials never appear here.
	const { form } = superForm(defaults(zod4(moderationConfigFormSchema)), { SPA: true, resetForm: false, validators: zod4Client(moderationConfigFormSchema) });
	let draftVersionId = $state('');
	let formError = $state('');

	const isLoading = $derived(moderationConfigService.isBusy);
	const setup = $derived(moderationConfigService.setup);
	const models = $derived(moderationConfigService.models);
	const active = $derived(moderationConfigService.activeConfiguration);
	const testResult = $derived(moderationConfigService.testResult);

	async function loadAll(): Promise<void> {
		try {
			await moderationConfigService.loadAll();
		} catch {
			formError = describeModerationConfigError(moderationConfigService.error);
		} finally {
			if (moderationConfigService.activeConfiguration) {
				$form = hydrateModerationConfig(moderationConfigService.activeConfiguration);
			} else {
 $form.systemMessage = moderationConfigService.setup?.defaultSystemMessage ?? '';
 }
		}
	}

	async function saveDraft(): Promise<void> {
		formError = '';
 const validated = moderationConfigFormSchema.safeParse($form);
 if (!validated.success) { formError = validated.error.issues[0]?.message ?? 'Check the configuration.'; return; }
		let parameters;
		try {
			parameters = moderationParametersFromForm($form);
		} catch (cause) {
			formError = cause instanceof ParameterValidationError ? cause.message : describeModerationConfigError(cause);
			return;
		}
		if ($form.modelId.trim() === '') {
			formError = 'Choose a model before saving a draft.';
			return;
		}
		try {
			draftVersionId = await moderationConfigService.saveDraft(
				$form.modelId.trim(),
				parameters,
				Number($form.outputMode) as ModerationOutputMode,
 $form.systemMessage
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
		if ($form.sampleComment.trim() === '') {
			formError = 'Provide a short sample comment for the test.';
			return;
		}
		const testedDraft = draftVersionId;
		try {
			await moderationConfigService.testDraft(testedDraft, $form.sampleComment.trim());
		} catch {
			if (testedDraft === draftVersionId) formError = describeModerationConfigError(moderationConfigService.error);
		}
	}

	async function activateDraft(): Promise<void> {
		formError = '';
		if (draftVersionId === '') {
			formError = 'Save and test a draft before activating it.';
			return;
		}
		try {
			await moderationConfigService.activateDraft(draftVersionId, active?.versionId ?? '', $form.resumePausedJobs);
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

	onMount(() => {
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
					No server-side model credential is configured. You can still save drafts. Reviews remain queued without consuming attempts until an operator sets the credential.
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
						bind:value={$form.modelId}
						options={models.map((model) => ({ value: model.modelId, text: model.modelId }))}
					/>
					<div class="form-actions">
						<Button type="button" variant="text" disabled={moderationConfigService.isLoading('models')} onclick={() => moderationConfigService.loadModels()}>
							{moderationConfigService.isLoading('models') ? 'Discovering…' : 'Refresh model list'}
						</Button>
					</div>
				</div>

				<div class="field-grid">
					<TextFieldOutlined label="Temperature" name="temperature" bind:value={$form.temperatureText} placeholder="provider default" />
					<TextFieldOutlined label="top_p" name="topP" bind:value={$form.topPText} placeholder="provider default" />
					<TextFieldOutlined label="Presence penalty" name="presencePenalty" bind:value={$form.presencePenaltyText} placeholder="provider default" />
					<TextFieldOutlined label="Frequency penalty" name="frequencyPenalty" bind:value={$form.frequencyPenaltyText} placeholder="provider default" />
				</div>

				<div class="field-grid">
					<div class="field">
						<SelectOutlined
							label="Token budget field"
							bind:value={$form.budgetKind}
							options={[
								{ value: 'none', text: 'Not set' },
								{ value: 'max_completion_tokens', text: 'max_completion_tokens' },
								{ value: 'max_tokens', text: 'max_tokens' }
							]}
						/>
					</div>
					{#if $form.budgetKind !== 'none'}
						<TextFieldOutlined label="Token budget" name="budget" bind:value={$form.budgetText} />
					{/if}
					<TextFieldOutlined label="Seed" name="seed" bind:value={$form.seedText} placeholder="provider default" />
					<TextFieldOutlined label="Reasoning effort" name="reasoningEffort" bind:value={$form.reasoningEffort} placeholder="provider default" />
				</div>

				<TextFieldOutlined label="Stop sequences (JSON array)" name="stop" bind:value={$form.stopText} placeholder="provider default" />

				<div class="field">
					<SelectOutlined
						label="Output mode"
						bind:value={$form.outputMode}
						options={[
							{ value: String(ModerationOutputMode.JSON_SCHEMA), text: 'Strict JSON schema (preferred)' },
							{ value: String(ModerationOutputMode.JSON_OBJECT), text: 'JSON mode (locally validated)' }
						]}
					/>
					<small class="field-meta">
						Both modes are validated locally; strict schema is only used when the model supports it.
					</small>
				</div>

                <TextFieldOutlinedMultiline label="System message" name="systemMessage" rows={8} bind:value={$form.systemMessage} />
                <small class="field-meta">The review rubric saved with this configuration. Leave empty to use the default rubric.</small>

				<TextFieldOutlinedMultiline
					label="Advanced parameters (name=value per line, allowlisted)"
					name="advanced"
					rows={3}
					bind:value={$form.advancedText}
					placeholder={"top_k=40\nmin_p=0.1"}
				/>

				<label><Checkbox><input type="checkbox" bind:checked={$form.resumePausedJobs} /></Checkbox>
					Resume jobs paused by configuration errors with this version (starts a new generation)
				</label>
				<div class="field-actions">
					<Button type="button" disabled={moderationConfigService.isLoading('save')} onclick={() => void saveDraft()}>
						{moderationConfigService.isLoading('save') ? 'Saving…' : 'Save draft'}
					</Button>
					{#if draftVersionId}
						<span class="draft-chip">Draft {draftVersionId.slice(0, 8)}…</span>
					{/if}
				</div>

				<div class="field">
					<TextFieldOutlinedMultiline label="Sample comment for test" name="sample" rows={2} bind:value={$form.sampleComment} />
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
