<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Button, Card, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setError, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
	import { authService, creditAccountApi, orderApi } from '$lib/services';
	import LocationPicker from '$lib/components/order/LocationPicker.svelte';
	import { creationSchema, recoverSubmission, saveSubmission, removeSubmission, putImage, validateImage, imageTypes, type SubmittedCreation } from '$lib/services/order/creation';
	import { describeOrderError, isDefinitiveRejection } from '$lib/services/order/errors';
	import type { CreateOrderInput } from '$lib/services/order';

	type Upload = { id: string; file: File; imageId?: string; state: 'uploading' | 'uploaded' | 'unknown' | 'failed'; error?: string };
	let uploads = $state<Upload[]>([]);
	let submission = $state<SubmittedCreation | null>(null);
	let recoveryError = $state('');
	let previousSubmissionReviewed = $state(false);
	let feedback = $state('Not submitted.');
	let outcome = $state<'not-submitted' | 'pending' | 'succeeded' | 'rejected' | 'failed'>('not-submitted');
	let busy = $state(false);
	let available = $state<bigint | null>(null);
	let balanceError = $state('');
	let balanceRequest = 0;
	let initialized = $state(false);
	const suspended = $derived(authService.user?.role === UserRole.SUSPENDED_USER);

	function localDate(value: Date) {
		return new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
	}

	const { form, errors, enhance, reset } = superForm(
		defaults({ pickupLocationId: '', deliveryLocationId: '', instructions: '', itemDescription: '', rewardCredits: '', deliveryMinutes: 30, expiresAt: localDate(new Date(Date.now() + 3600000)) }, zod4(creationSchema)),
		{ SPA: true, validators: zod4Client(creationSchema), resetForm: false, multipleSubmits: 'prevent',
			async onUpdate({ form: validated }) {
				if (!validated.valid || submission || busy || suspended || recoveryError) return;
				if (new Date(validated.data.expiresAt).getTime() <= Date.now()) {
					setError(validated, 'expiresAt', 'Choose an expiry time in the future.');
					return;
				}
				if (uploads.some((image) => image.state === 'uploading' || image.state === 'failed' || !image.imageId)) {
					setMessage(validated, 'Retry or explicitly remove each failed image before submitting.');
					feedback = 'Not submitted. Retry or remove failed images first.';
					return;
				}
				const input: CreateOrderInput = { ...validated.data, expiresAt: new Date(validated.data.expiresAt).toISOString(), itemImageIds: uploads.map((image) => image.imageId!) };
				const callerId = authService.user?.id;
				if (!callerId) return;
				const value: SubmittedCreation = { version: 1, callerId, key: crypto.randomUUID(), submittedAt: Date.now(), input };
				try { saveSubmission(sessionStorage, value); }
				catch { recoveryError = 'Could not save the retry identity. Enable tab storage before submitting. No Order request was sent.'; return; }
				submission = value;
				await sendSubmission();
			}
		}
	);

	async function loadBalance() {
		const request = ++balanceRequest;
		available = null;
		balanceError = '';
		const caller = authService.user?.id;
		if (!caller) return;
		try {
			const balance = await creditAccountApi.getAvailableCredits();
			if (request === balanceRequest && authService.user?.id === caller) available = balance;
		} catch { if (request === balanceRequest && authService.user?.id === caller) balanceError = 'Balance unavailable. The backend will check credits when you submit.'; }
	}

	async function sendSubmission() {
		const current = submission;
		if (!current || busy || current.callerId !== authService.user?.id) return;
		const recovered = recoverSubmission(sessionStorage, current.callerId);
		if (recovered.state !== 'ready' || recovered.submission.key !== current.key) {
			recoveryError = recovered.state === 'blocked' ? recovered.message : 'Saved retry identity changed. Check My Orders before continuing.';
			return;
		}
		busy = true;
		outcome = 'pending';
		feedback = 'Pending. Checking the original submission. Do not start a duplicate Order.';
		try {
			const order = await orderApi.createOrder(current.input, current.key);
			if (authService.user?.id !== current.callerId) return;
			if (order.status === OrderStatus.PENDING_RESERVATION) {
				submission = { ...current, orderId: order.id };
				saveSubmission(sessionStorage, submission);
				feedback = 'Pending reservation. This Order is saved but is not open to couriers yet. Check the same submission again or view its current status.';
			} else {
				removeSubmission(sessionStorage, current.callerId);
				submission = null;
				outcome = 'succeeded';
				feedback = 'Succeeded. Opening the created Order.';
				await goto('/orders/' + encodeURIComponent(order.id));
			}
		} catch (cause) {
			if (authService.user?.id !== current.callerId) return;
			if (isDefinitiveRejection(cause)) {
				try { removeSubmission(sessionStorage, current.callerId); } catch { recoveryError = 'Could not clear the resolved submission. Check My Orders before another submission.'; }
				submission = null;
				for (const image of uploads) {
					if (image.state === 'unknown') { image.state = 'failed'; image.error = 'Creation was rejected. Retry this uncertain upload or explicitly remove it.'; }
				}
				outcome = 'rejected';
				feedback = 'Rejected. ' + describeOrderError(cause, 'create this order');
			} else {
				outcome = 'failed';
				feedback = 'Failed to confirm the result. The Order may already exist. Retry the same submission. ' + describeOrderError(cause, 'confirm this submission');
			}
		} finally { busy = false; }
	}

	function startAfterReview() {
		const caller = authService.user?.id;
		if (!caller || busy || !previousSubmissionReviewed || !recoveryError) return;
		try {
			removeSubmission(sessionStorage, caller);
			submission = null;
			recoveryError = '';
			previousSubmissionReviewed = false;
			outcome = 'not-submitted';
			feedback = 'Not submitted. You can start another order after reviewing the previous result.';
		} catch {
			recoveryError = 'Could not clear the previous submission. Keep checking My Orders before submitting again.';
		}
	}

	async function upload(image: Upload) {
		if (submission || busy) return;
		image.state = 'uploading';
		image.error = undefined;
		const validation = validateImage(image.file);
		if (validation) { image.state = 'failed'; image.error = validation; return; }
		try {
			const grant = await orderApi.createItemImageUpload(image.file.type, image.file.size);
			image.imageId = grant.imageId;
			image.state = await putImage(image.file, grant);
		} catch (cause) {
			image.state = 'failed';
			image.error = cause instanceof Error ? cause.message : 'Image upload failed. Retry or remove this image.';
		}
	}

	function addImages(event: Event) {
		const field = event.currentTarget as HTMLInputElement;
		const selected = [...(field.files ?? [])];
		if (uploads.length + selected.length > 5) {
			feedback = 'Not submitted. Select at most five images. No new files were added.';
			field.value = '';
			return;
		}
		for (const file of selected) {
			const image: Upload = { id: crypto.randomUUID(), file, state: 'uploading' };
			uploads.push(image);
			// Use the reactive entry so progress updates are visible in the UI.
			void upload(uploads[uploads.length - 1]);
		}
		field.value = '';
	}

	$effect(() => {
		const caller = authService.user?.id;
		previousSubmissionReviewed = false;
		if (!initialized) return;
		void loadBalance();
		if (!caller) return;
		const restored = recoverSubmission(sessionStorage, caller);
		if (restored.state === 'blocked') recoveryError = restored.message;
		if (restored.state === 'ready') {
			submission = restored.submission;
			const input = restored.submission.input;
			reset({ data: { ...input, expiresAt: localDate(new Date(input.expiresAt)) } });
			outcome = 'pending';
			feedback = 'Recovered a submitted Order. Check/retry this same submission before creating another.';
		}
	});

	onMount(() => { initialized = true; });
</script>

<svelte:head><title>Create Order · Friend on Campus</title></svelte:head>
<section class="page-content" aria-labelledby="create-title">
	<header class="page-heading"><div><p class="eyebrow">Orders</p><h1 id="create-title">Create Order</h1><p>Describe the item and choose where it should be picked up and delivered.</p></div></header>
	<nav><a href="/orders">Browse</a> · <a href="/orders/mine">My Orders</a></nav>
	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to create an order.</p>
	{:else if suspended}<p class="notice">Your account is suspended. Creation is unavailable. You can still read <a href="/orders/mine">My Orders</a>.</p>
	{:else}
		{#if recoveryError}
			<p class="notice error" role="alert">{recoveryError} <a href="/orders/mine" target="_blank" rel="noopener">Check My Orders</a>.</p>
			<label><input type="checkbox" bind:checked={previousSubmissionReviewed} /> I checked My Orders and confirmed the previous request is resolved.</label>
			<Button variant="text" disabled={busy || !previousSubmissionReviewed} onclick={startAfterReview}>Start another order</Button>
		{/if}
		<p class:failure={outcome === 'failed' || outcome === 'rejected'} class="notice" role="status" aria-live="polite">{feedback}</p>
		{#if submission}
			<Card variant="outlined"><div class="panel-body">
				<p>Saved submission: {submission.key}</p>
				<p>{submission.input.itemImageIds.length} image references are retained. They do not need re-uploading to retry this submission.</p>
				<Button disabled={busy || !!recoveryError} onclick={() => void sendSubmission()}>{busy ? 'Checking…' : 'Check/retry same submission'}</Button>
				{#if submission.orderId}<a href={'/orders/' + submission.orderId}>View saved Order</a>{/if}
			</div></Card>
		{/if}
		<Card variant="outlined"><div class="panel-body">
			{#if available !== null}<p>Available credits: <strong>{available.toString()}</strong>. Reservation at submission is authoritative.</p>{/if}
			{#if balanceError}<p class="notice">{balanceError}</p><Button variant="text" onclick={() => void loadBalance()}>Retry balance</Button>{/if}
			<form method="POST" use:enhance>
				<fieldset disabled={busy || !!submission || !!recoveryError}>
					<input type="hidden" name="pickupLocationId" value={$form.pickupLocationId} />
					<input type="hidden" name="deliveryLocationId" value={$form.deliveryLocationId} />
					<LocationPicker label="Pickup location" bind:value={$form.pickupLocationId} />
					{#if $errors.pickupLocationId}<p class="field-error" role="alert">{$errors.pickupLocationId[0]}</p>{/if}
					<LocationPicker label="Delivery location" bind:value={$form.deliveryLocationId} />
					{#if $errors.deliveryLocationId}<p class="field-error" role="alert">{$errors.deliveryLocationId[0]}</p>{/if}
					<div class="field"><TextFieldOutlinedMultiline label="Item description" name="itemDescription" bind:value={$form.itemDescription} required error={!!$errors.itemDescription} aria-invalid={$errors.itemDescription ? 'true' : undefined} />{#if $errors.itemDescription}<small class="field-error" role="alert">{$errors.itemDescription[0]}</small>{/if}</div>
					<div class="field"><TextFieldOutlinedMultiline label="Delivery instructions (optional)" name="instructions" bind:value={$form.instructions} error={!!$errors.instructions} />{#if $errors.instructions}<small class="field-error" role="alert">{$errors.instructions[0]}</small>{/if}</div>
					<div class="field"><TextFieldOutlined label="Reward in whole credits" name="rewardCredits" inputmode="numeric" bind:value={$form.rewardCredits} required error={!!$errors.rewardCredits} />{#if $errors.rewardCredits}<small class="field-error" role="alert">{$errors.rewardCredits[0]}</small>{/if}</div>
					<label>Allowed delivery time from pickup (minutes)<input type="number" name="deliveryMinutes" min="5" max="1440" step="1" bind:value={$form.deliveryMinutes} required />{#if $errors.deliveryMinutes}<small class="field-error" role="alert">{$errors.deliveryMinutes[0]}</small>{/if}</label>
					<label>Unaccepted Order expiry (your local time)<input type="datetime-local" name="expiresAt" bind:value={$form.expiresAt} required />{#if $errors.expiresAt}<small class="field-error" role="alert">{$errors.expiresAt[0]}</small>{/if}</label>
					<label>Item images (optional, maximum five, 10 MB each)<input type="file" multiple accept={imageTypes.join(',')} onchange={addImages} /></label>
					<ul class="uploads">{#each uploads as image (image.id)}<li>
						<strong>{image.file.name}</strong><span>{image.state === 'unknown' ? 'Upload status unknown. CreateOrder will verify this image when you submit.' : image.state}</span>
						{#if image.error}<p class="field-error" role="alert">{image.error}</p>{/if}
						<div class="form-actions">{#if image.state !== 'uploading'}<Button type="button" variant="text" onclick={() => void upload(image)}>Retry image upload</Button><Button type="button" variant="text" onclick={() => uploads = uploads.filter((item) => item.id !== image.id)}>Remove image</Button>{/if}</div>
					</li>{/each}</ul>
					<Button type="submit" disabled={!initialized || uploads.some((image) => image.state === 'uploading')}>Create Order</Button>
				</fieldset>
			</form>
		</div></Card>
	{/if}
</section>

<style>
	fieldset { display: grid; gap: 1.25rem; border: 0; margin: 0; padding: 0; min-width: 0; }
	label { display: grid; gap: .5rem; }
	input { min-width: 0; max-width: 100%; padding: .75rem; font: inherit; }
	input:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	.uploads { padding: 0; list-style: none; display: grid; gap: .75rem; }
	.uploads li { display: grid; gap: .5rem; overflow-wrap: anywhere; border: 1px solid var(--m3c-outline-variant); border-radius: 8px; padding: .75rem; }
	.failure { background: var(--m3c-error-container); color: var(--m3c-on-error-container); }
</style>
