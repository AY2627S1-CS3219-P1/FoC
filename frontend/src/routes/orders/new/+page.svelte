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
	let feedback = $state('');
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
					setMessage(validated, 'Retry or remove failed images before submitting.');
					feedback = 'Retry or remove failed images before submitting.';
					return;
				}
				const input: CreateOrderInput = { ...validated.data, expiresAt: new Date(validated.data.expiresAt).toISOString(), itemImageIds: uploads.map((image) => image.imageId!) };
				const callerId = authService.user?.id;
				if (!callerId) return;
				const value: SubmittedCreation = { version: 1, callerId, key: crypto.randomUUID(), submittedAt: Date.now(), input };
				try { saveSubmission(sessionStorage, value); }
				catch { recoveryError = 'Your browser could not save this order. Allow website storage and try again. No order was created.'; return; }
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
		} catch { if (request === balanceRequest && authService.user?.id === caller) balanceError = 'Balance unavailable. Your credits will be checked when you submit.'; }
	}

	async function sendSubmission() {
		const current = submission;
		if (!current || busy || current.callerId !== authService.user?.id) return;
		const recovered = recoverSubmission(sessionStorage, current.callerId);
		if (recovered.state !== 'ready' || recovered.submission.key !== current.key) {
			recoveryError = recovered.state === 'blocked' ? recovered.message : 'Your saved order has changed. Check My Orders before continuing.';
			return;
		}
		busy = true;
		outcome = 'pending';
		feedback = 'Checking your order. Do not create another order yet.';
		try {
			const order = await orderApi.createOrder(current.input, current.key);
			if (authService.user?.id !== current.callerId) return;
			if (order.status === OrderStatus.PENDING_RESERVATION) {
				submission = { ...current, orderId: order.id };
				saveSubmission(sessionStorage, submission);
				feedback = 'Reserving credits for your order. Couriers can accept it once this is complete.';
			} else {
				removeSubmission(sessionStorage, current.callerId);
				submission = null;
				outcome = 'succeeded';
				feedback = 'Order created. Opening it…';
				await goto('/orders/' + encodeURIComponent(order.id));
			}
		} catch (cause) {
			if (authService.user?.id !== current.callerId) return;
			if (isDefinitiveRejection(cause)) {
				try { removeSubmission(sessionStorage, current.callerId); } catch { recoveryError = 'Could not finish saving your order status. Check My Orders before creating another order.'; }
				submission = null;
				for (const image of uploads) {
					if (image.state === 'unknown') { image.state = 'failed'; image.error = 'Order not created. Try uploading this image again or remove it.'; }
				}
				outcome = 'rejected';
				feedback = 'Order not created. ' + describeOrderError(cause, 'create this order');
			} else {
				outcome = 'failed';
				feedback = 'Could not confirm your order. It may already exist. Choose Check order to try again safely. ' + describeOrderError(cause, 'confirm this order');
			}
		} finally { busy = false; }
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
			feedback = 'Choose up to five images. No new files were added.';
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
			feedback = 'Your last order still needs confirmation. Check it before creating another order.';
		}
	});

	onMount(() => { initialized = true; });
</script>

<svelte:head><title>Create order · Friend on Campus</title></svelte:head>
<section class="page-content" aria-labelledby="create-title">
	<header class="page-heading"><div><h1 id="create-title">Create order</h1></div></header>
	<nav><a href="/orders">Browse</a> · <a href="/orders/mine">My Orders</a></nav>
	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to create an order.</p>
	{:else if suspended}<p class="notice">Your account is suspended. You can still view <a href="/orders/mine">My Orders</a>.</p>
	{:else}
		{#if recoveryError}<p class="notice error" role="alert">{recoveryError} <a href="/orders/mine">Check My Orders</a>.</p>{/if}
		<p class:empty={!feedback} class:failure={outcome === 'failed' || outcome === 'rejected'} class="notice" role="status" aria-live="polite">{feedback}</p>
		{#if submission}
			<Card variant="outlined"><div class="panel-body">
				<Button disabled={busy || !!recoveryError} onclick={() => void sendSubmission()}>{busy ? 'Checking…' : 'Check order'}</Button>
				{#if submission.orderId}<a href={'/orders/' + submission.orderId}>View order</a>{/if}
			</div></Card>
		{/if}
		<Card variant="outlined"><div class="panel-body">
			{#if available !== null}<p>Available credits: <strong>{available.toString()}</strong>. Credits are checked and reserved when you submit.</p>{/if}
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
					<label>Time to deliver after pickup (minutes)<input type="number" name="deliveryMinutes" min="5" max="1440" step="1" bind:value={$form.deliveryMinutes} required />{#if $errors.deliveryMinutes}<small class="field-error" role="alert">{$errors.deliveryMinutes[0]}</small>{/if}</label>
					<div class="field"><TextFieldOutlined label="Accept by (your local time)" type="datetime-local" name="expiresAt" bind:value={$form.expiresAt} required error={!!$errors.expiresAt} aria-invalid={$errors.expiresAt ? 'true' : undefined} />{#if $errors.expiresAt}<small class="field-error" role="alert">{$errors.expiresAt[0]}</small>{/if}</div>
					<label>Item images (optional, maximum five, 10 MB each)<input type="file" multiple accept={imageTypes.join(',')} onchange={addImages} /></label>
					<ul class="uploads">{#each uploads as image (image.id)}<li>
						<strong>{image.file.name}</strong><span>{image.state === 'unknown' ? 'We will check this image when you create your order.' : image.state}</span>
						{#if image.error}<p class="field-error" role="alert">{image.error}</p>{/if}
						<div class="form-actions">{#if image.state !== 'uploading'}<Button type="button" variant="text" onclick={() => void upload(image)}>Retry</Button><Button type="button" variant="text" onclick={() => uploads = uploads.filter((item) => item.id !== image.id)}>Remove</Button>{/if}</div>
					</li>{/each}</ul>
					<Button type="submit" disabled={!initialized || uploads.some((image) => image.state === 'uploading')}>Create order</Button>
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
	input[type="number"] { box-sizing: border-box; width: 100%; min-height: 3.5rem; padding: 1rem; border: 1px solid var(--m3c-outline); border-radius: var(--m3-field-outlined-shape, .25rem); background: transparent; color: var(--m3c-on-surface); }
	input[type="number"]:hover { border-color: var(--m3c-on-surface); }
	input[type="number"]:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: -2px; border-color: var(--m3c-primary); }
	input[type="number"]:disabled { opacity: .38; }
	.uploads { padding: 0; list-style: none; display: grid; gap: .75rem; }
	.uploads li { display: grid; gap: .5rem; overflow-wrap: anywhere; border: 1px solid var(--m3c-outline-variant); border-radius: 8px; padding: .75rem; }
	.notice.empty { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
	.failure { background: var(--m3c-error-container); color: var(--m3c-on-error-container); }
</style>
