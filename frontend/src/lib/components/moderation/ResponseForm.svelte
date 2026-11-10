<script lang="ts">
	import { Button, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { snackbarService } from '$lib/services';
	import { characterCount, describeModerationError, type CreateEvidenceUpload } from '$lib/services/moderation';
	import { responseSchema } from '$lib/services/moderation/schemas';
	import EvidenceField from './EvidenceField.svelte';

	// The participant's reply on a report or appeal, with optional evidence.
	let {
		heading,
		help,
		upload,
		onsubmit
	}: {
		heading: string;
		help: string;
		upload: CreateEvidenceUpload;
		onsubmit: (body: string, evidenceIds: string[]) => Promise<void>;
	} = $props();

	let evidenceField: EvidenceField | undefined = $state();
	// Remounts the evidence picker after a successful reply.
	let attempt = $state(0);

	const formId = $props.id();
	const { form, errors, enhance, submitting, message } = superForm(defaults(zod4(responseSchema)), {
		id: formId,
		SPA: true,
		validators: zod4Client(responseSchema),
		validationMethod: 'onblur',
		multipleSubmits: 'prevent',
		resetForm: false,
		async onUpdate({ form: validated }) {
			if (!validated.valid) return;
			const evidenceIds = evidenceField?.readyEvidenceIds();
			if (!evidenceIds) return;
			try {
				await onsubmit(validated.data.body, evidenceIds);
				validated.data.body = '';
				attempt++;
				snackbarService.show('Response sent.', { severity: 'success' });
			} catch (cause) {
				setMessage(validated, describeModerationError(cause, 'send your response'));
			}
		}
	});
</script>

<form class="response-form" method="POST" use:enhance aria-labelledby={`${formId}-title`}>
	<h2 id={`${formId}-title`}>{heading}</h2>
	<p class="secondary-text">{help}</p>
	<TextFieldOutlinedMultiline
		label="Your response"
		name="body"
		rows={4}
		bind:value={$form.body}
		error={!!$errors.body}
		aria-invalid={$errors.body ? 'true' : undefined}
		aria-describedby={`${formId}-body-meta`}
	/>
	<small class="field-meta" id={`${formId}-body-meta`}>
		{#if $errors.body}<span class="field-error">{$errors.body[0]}</span>{/if}
		{characterCount($form.body).toLocaleString('en-SG')}/4,000
	</small>
	{#key attempt}
		<EvidenceField bind:this={evidenceField} {upload} disabled={$submitting} />
	{/key}
	{#if $message}<p class="notice error" role="alert">{$message}</p>{/if}
	<div class="form-actions">
		<Button type="submit" disabled={$submitting}>{$submitting ? 'Sending…' : 'Send response'}</Button>
	</div>
</form>

<style>
	.response-form { display: grid; gap: 0.75rem; min-width: 0; }
	h2 { margin: 0; font-size: 1.1rem; }
	p { margin: 0; }
	.field-meta { display: flex; justify-content: space-between; gap: 1rem; }
</style>
