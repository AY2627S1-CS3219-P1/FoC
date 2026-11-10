<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, Icon, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import iconPhoto from '@ktibow/iconset-material-symbols/add-photo-alternate-outline';
	import { defaults, setError, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
	import type { Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
	import { authService, creditAccountApi, orderApi } from '$lib/services';
	import LocationPicker from '$lib/components/order/LocationPicker.svelte';
	import CampusMap, { type MapMarker } from '$lib/components/map/CampusMap.svelte';
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
		} else {
			// "Request" links from a supplier pre-fill the pickup.
			const linked = page.url.searchParams.get('pickup');
			if (linked && !$form.pickupLocationId) $form.pickupLocationId = linked;
		}
	});

	onMount(() => { initialized = true; });

	// The map follows the chosen locations; both are Supplier Locations with coordinates.
	let pickupLocation = $state<Location | null>(null);
	let deliveryLocation = $state<Location | null>(null);
	const markers = $derived.by<MapMarker[]>(() => {
		const out: MapMarker[] = [];
		const pick = pickupLocation?.id === $form.pickupLocationId ? pickupLocation : null;
		const drop = deliveryLocation?.id === $form.deliveryLocationId ? deliveryLocation : null;
		if (pick?.coordinates) out.push({ id: 'pickup', latitude: pick.coordinates.latitude, longitude: pick.coordinates.longitude, label: 'Pick up: ' + pick.name, kind: 'pickup' });
		if (drop?.coordinates) out.push({ id: 'destination', latitude: drop.coordinates.latitude, longitude: drop.coordinates.longitude, label: 'Deliver to: ' + drop.name, kind: 'destination' });
		return out;
	});
</script>

<svelte:head><title>Make a request · Friend on Campus</title></svelte:head>
<section class="create" aria-labelledby="create-title">
	<header class="heading">
		<h1 id="create-title">Make a request</h1>
		{#if available !== null}<p class="muted">You have {available.toString()} credits available.</p>{/if}
	</header>
	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to make a request.</p>
	{:else if suspended}<p class="notice">Your account is suspended. You can still view <a href="/orders/mine">your orders</a>.</p>
	{:else}
		{#if recoveryError}<p class="notice error" role="alert">{recoveryError} <a href="/orders/mine">Check your orders</a>.</p>{/if}
		<p class:empty={!feedback} class:failure={outcome === 'failed' || outcome === 'rejected'} class="notice" role="status" aria-live="polite">{feedback}</p>
		{#if submission}
			<div class="pending">
				<Button disabled={busy || !!recoveryError} onclick={() => void sendSubmission()}>{busy ? 'Checking…' : 'Check order'}</Button>
				{#if submission.orderId}<a href={'/orders/' + submission.orderId}>View order</a>{/if}
			</div>
		{/if}
		{#if balanceError}<p class="notice">{balanceError} <Button variant="text" size="s" onclick={() => void loadBalance()}>Retry</Button></p>{/if}

		<div class="layout">
			<!-- Wireframe: the route map sits beside the form (above it on phones). -->
			<aside class="map" aria-label="Route preview">
				<CampusMap {markers} route={['pickup', 'destination']} label="Map from pickup to delivery location" />
				{#if !markers.length}<p class="map-hint">Choose a pickup and delivery location to see the route.</p>{/if}
			</aside>

			<div class="form-card"><Card variant="outlined">
				<form method="POST" use:enhance class="panel-body">
					<fieldset disabled={busy || !!submission || !!recoveryError}>
						<input type="hidden" name="pickupLocationId" value={$form.pickupLocationId} />
						<input type="hidden" name="deliveryLocationId" value={$form.deliveryLocationId} />

						<section class="group" aria-labelledby="what-title">
							<h2 id="what-title">What do you need?</h2>
							<div class="field"><TextFieldOutlinedMultiline label="Item description" name="itemDescription" bind:value={$form.itemDescription} required error={!!$errors.itemDescription} aria-invalid={$errors.itemDescription ? 'true' : undefined} />{#if $errors.itemDescription}<small class="field-error" role="alert">{$errors.itemDescription[0]}</small>{/if}</div>
							<div class="photos">
								<Button variant="outlined" iconType="left" label>
									<Icon icon={iconPhoto} />Add photos
									<input class="file" type="file" multiple accept={imageTypes.join(',')} onchange={addImages} />
								</Button>
								<small class="muted">Optional. Up to five photos, 10 MB each.</small>
							</div>
							{#if uploads.length}
								<ul class="uploads">{#each uploads as image (image.id)}<li>
									<span class="file-name">{image.file.name}</span>
									<span class="muted">{image.state === 'unknown' ? 'Checked when you submit' : image.state === 'uploaded' ? 'Uploaded' : image.state === 'uploading' ? 'Uploading…' : 'Failed'}</span>
									{#if image.error}<p class="field-error" role="alert">{image.error}</p>{/if}
									{#if image.state !== 'uploading'}<span class="upload-actions"><Button type="button" variant="text" size="s" onclick={() => void upload(image)}>Retry</Button><Button type="button" variant="text" size="s" onclick={() => uploads = uploads.filter((item) => item.id !== image.id)}>Remove</Button></span>{/if}
								</li>{/each}</ul>
							{/if}
						</section>

						<section class="group" aria-labelledby="where-title">
							<h2 id="where-title">Where</h2>
							<LocationPicker label="Pick up from" bind:value={$form.pickupLocationId} onresolve={(location) => (pickupLocation = location)} />
							{#if $errors.pickupLocationId}<p class="field-error" role="alert">{$errors.pickupLocationId[0]}</p>{/if}
							<LocationPicker label="Deliver to" bind:value={$form.deliveryLocationId} onresolve={(location) => (deliveryLocation = location)} />
							{#if $errors.deliveryLocationId}<p class="field-error" role="alert">{$errors.deliveryLocationId[0]}</p>{/if}
							<div class="field"><TextFieldOutlinedMultiline label="Delivery instructions (optional)" name="instructions" bind:value={$form.instructions} error={!!$errors.instructions} />{#if $errors.instructions}<small class="field-error" role="alert">{$errors.instructions[0]}</small>{:else}<small class="muted">Only your courier sees these and the exact delivery location.</small>{/if}</div>
						</section>

						<section class="group" aria-labelledby="reward-title">
							<h2 id="reward-title">Reward and timing</h2>
							<div class="row">
								<div class="field"><TextFieldOutlined label="Reward (credits)" name="rewardCredits" inputmode="numeric" bind:value={$form.rewardCredits} required error={!!$errors.rewardCredits} />{#if $errors.rewardCredits}<small class="field-error" role="alert">{$errors.rewardCredits[0]}</small>{:else}<small class="muted">Reserved when you submit. Couriers can counteroffer higher.</small>{/if}</div>
								<div class="field">
									<TextFieldOutlined label="Minutes to deliver after pickup" name="deliveryMinutes" type="number" min="5" max="1440" step="1" inputmode="numeric" required value={String($form.deliveryMinutes)} oninput={(event) => ($form.deliveryMinutes = Number(event.currentTarget.value))} error={!!$errors.deliveryMinutes} />
									{#if $errors.deliveryMinutes}<small class="field-error" role="alert">{$errors.deliveryMinutes[0]}</small>{:else}<small class="muted">Late deliveries earn the courier less.</small>{/if}
								</div>
							</div>
							<div class="field"><TextFieldOutlined label="Find a courier by (your local time)" type="datetime-local" name="expiresAt" bind:value={$form.expiresAt} required error={!!$errors.expiresAt} aria-invalid={$errors.expiresAt ? 'true' : undefined} />{#if $errors.expiresAt}<small class="field-error" role="alert">{$errors.expiresAt[0]}</small>{:else}<small class="muted">The request closes if no one accepts it by then, and your credits are returned.</small>{/if}</div>
						</section>

						<div class="submit">
							<Button type="submit" disabled={!initialized || uploads.some((image) => image.state === 'uploading')}>Submit request</Button>
						</div>
					</fieldset>
				</form>
			</Card></div>
		</div>
	{/if}
</section>

<style>
	.create { display: grid; gap: 1rem; max-width: 76rem; margin: 0 auto; }
	.heading { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: 0.25rem 1rem; }
	h1 { margin: 0; font-size: clamp(1.6rem, 3vw, 2rem); }
	h2 { margin: 0; font-size: 1.1rem; }
	.muted { margin: 0; color: var(--m3c-on-surface-variant); font-size: 0.875rem; }
	.pending { display: flex; align-items: center; gap: 1rem; }

	.layout { display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); gap: 1.5rem; align-items: start; }
	.form-card { grid-column: 1; grid-row: 1; display: grid; min-width: 0; }
	.map { grid-column: 2; grid-row: 1; position: sticky; top: 1rem; height: min(70vh, 34rem); }
	.map-hint {
		position: absolute; left: 0.75rem; right: 0.75rem; bottom: 0.75rem; z-index: 10; /* Above the map panel's internals, below the top bar (30) and drawer (50). */ margin: 0;
		padding: 0.5rem 0.75rem; border-radius: var(--m3-shape-medium);
		background: var(--m3c-surface-container-lowest, #fff); font-size: 0.875rem;
		box-shadow: 0 1px 4px rgb(0 0 0 / 0.25);
	}

	fieldset { display: grid; gap: 2rem; border: 0; margin: 0; padding: 0; min-width: 0; }
	.group { display: grid; gap: 1rem; min-width: 0; }
	.row { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; }
	.field { display: grid; gap: 0.35rem; min-width: 0; }


	.photos { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem 1rem; }
	.file { position: absolute; width: 1px; height: 1px; opacity: 0; pointer-events: none; }
	.uploads { padding: 0; margin: 0; list-style: none; display: grid; gap: 0.5rem; }
	.uploads li { display: flex; flex-wrap: wrap; align-items: center; gap: 0.25rem 0.75rem; overflow-wrap: anywhere; padding: 0.5rem 0.75rem; border-radius: var(--m3-shape-medium); background: var(--m3c-surface-container); }
	.file-name { font-weight: 600; min-width: 0; }
	.upload-actions { margin-inline-start: auto; display: flex; }
	.uploads .field-error { flex-basis: 100%; margin: 0; }

	.submit { display: flex; justify-content: flex-end; }
	.notice.empty { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
	.failure { background: var(--m3c-error-container); color: var(--m3c-on-error-container); }

	@media (max-width: 60rem) {
		.layout { grid-template-columns: minmax(0, 1fr); }
		.map { grid-column: 1; grid-row: 1; position: relative; height: 14rem; }
		.form-card { grid-row: 2; }
	}
	@media (max-width: 36rem) {
		.row { grid-template-columns: minmax(0, 1fr); }
	}
</style>
