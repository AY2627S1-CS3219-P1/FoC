<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, LoadingIndicator, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, profileService } from '$lib/services';
	import type { UserProfile } from '$lib/services/profile/profileApi';
	import { profileSchema, type ProfileFormData } from '$lib/services/profile/profileSchema';
	import { roleLabel } from '$lib/services/admin/roles';

	let profile = $state<UserProfile | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	function valuesFromProfile(value: UserProfile): ProfileFormData {
		return {
			displayName: value.displayName,
			description: value.description,
			telegramHandle: value.telegramHandle ?? '',
			phoneNumber: value.phoneNumber ?? ''
		};
	}

	const { form, errors, constraints, message, submitting, enhance, reset, isTainted, tainted } = superForm(
		defaults(zod4(profileSchema)),
		{
			SPA: true,
			validators: zod4Client(profileSchema),
			validationMethod: 'onblur',
			multipleSubmits: 'prevent',
			resetForm: false,
			taintedMessage: true,
			async onUpdate({ form: validated }) {
				if (!validated.valid) return;
				try {
					const updated = await profileService.updateMyProfile({
						displayName: validated.data.displayName,
						description: validated.data.description,
						telegramHandle: validated.data.telegramHandle || undefined,
						phoneNumber: validated.data.phoneNumber || undefined
					});
					profile = updated;
					const saved = valuesFromProfile(updated);
					reset({ data: saved, newState: saved });
					validated.data = saved;
					setMessage(validated, 'Your profile has been updated.');
				} catch (cause) {
					setMessage(validated, errorMessage(cause));
				}
			}
		}
	);

	function errorMessage(cause: unknown): string {
		if (cause instanceof ConnectError) {
			if (cause.code === Code.InvalidArgument) return 'The profile details were rejected. Check the fields and try again.';
			if (cause.code === Code.PermissionDenied) return 'Your account cannot edit its profile.';
			if (cause.code === Code.Unauthenticated) return 'Your session has expired. Sign in again.';
		}
		return 'We could not save your profile. Your changes are still here; please try again.';
	}

	async function loadProfile() {
		loading = true;
		loadError = '';
		try {
			profile = await profileService.getMyProfile();
			const saved = valuesFromProfile(profile);
			reset({ data: saved, newState: saved });
		} catch {
			loadError = 'We could not load your profile. Check your connection and try again.';
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void (async () => {
			if (!authService.isAuthenticated) {
				await goto('/login', { replaceState: true });
				return;
			}
			await loadProfile();
		})();
	});
</script>

<svelte:head><title>Your profile · Friend on Campus</title></svelte:head>

<section class="page-content profile-page" aria-labelledby="profile-title">
	<header class="page-heading">
		<div><p class="eyebrow">Your account</p><h1 id="profile-title">Your profile</h1></div>
		{#if profile}<span class="role-chip">{roleLabel(profile.role)}</span>{/if}
	</header>

	{#if loading}
		<div class="loading-state" role="status" aria-live="polite"><LoadingIndicator aria-label="Loading profile" /><p>Loading your profile…</p></div>
	{:else if loadError}
		<Card variant="outlined"><div class="panel-body"><p class="notice error" role="alert">{loadError}</p><div class="form-actions"><Button onclick={() => void loadProfile()}>Try again</Button></div></div></Card>
	{:else if profile}
		<Card variant="outlined">
			<div class="panel-body">
				<div class="account-facts"><div><span>Email</span><strong>{profile.email}</strong></div><div><span>Account role</span><strong>{roleLabel(profile.role)}</strong></div></div>
				{#if profile.role === UserRole.SUSPENDED_USER}
					<p class="notice" role="status">Your account is suspended. You can view your profile, but editing is unavailable.</p>
				<div class="account-facts"><div><span>Display name</span><strong>{profile.displayName}</strong></div><div><span>Description</span><strong>{profile.description || '—'}</strong></div><div><span>Telegram</span><strong>{profile.telegramHandle || '—'}</strong></div><div><span>Phone</span><strong>{profile.phoneNumber || '—'}</strong></div></div>
				{:else}
					{#if $message}<p class="notice" role="status" aria-live="polite">{$message}</p>{/if}
					<form class="data-form" method="POST" use:enhance>
						<div class="field"><TextFieldOutlined label="Display name" name="displayName" autocomplete="name" required bind:value={$form.displayName} maxlength={Number($constraints.displayName?.maxlength ?? 100)} error={!!$errors.displayName} aria-invalid={$errors.displayName ? 'true' : undefined} />{#if $errors.displayName}<small class="field-error">{$errors.displayName[0]}</small>{/if}</div>
						<div class="field"><TextFieldOutlinedMultiline label="Description" name="description" rows={5} bind:value={$form.description} maxlength={Number($constraints.description?.maxlength ?? 500)} error={!!$errors.description} aria-invalid={$errors.description ? 'true' : undefined} /><small class="field-meta">{Array.from($form.description).length}/500</small>{#if $errors.description}<small class="field-error">{$errors.description[0]}</small>{/if}</div>
						<div class="field"><TextFieldOutlined label="Telegram handle · optional, without @" name="telegramHandle" autocomplete="off" bind:value={$form.telegramHandle} maxlength={32} error={!!$errors.telegramHandle} aria-invalid={$errors.telegramHandle ? 'true' : undefined} />{#if $errors.telegramHandle}<small class="field-error">{$errors.telegramHandle[0]}</small>{/if}</div>
						<div class="field"><TextFieldOutlined label="Phone number · optional" name="phoneNumber" autocomplete="tel" bind:value={$form.phoneNumber} maxlength={20} error={!!$errors.phoneNumber} aria-invalid={$errors.phoneNumber ? 'true' : undefined} />{#if $errors.phoneNumber}<small class="field-error">{$errors.phoneNumber[0]}</small>{/if}</div>
						<div class="form-actions"><Button type="button" variant="outlined" disabled={$submitting || !isTainted($tainted)} onclick={() => reset()}>Cancel</Button><Button type="submit" disabled={$submitting || !isTainted($tainted) || Object.values($errors).some((value) => value?.length)}>{$submitting ? 'Saving…' : 'Save changes'}</Button></div>
					</form>
				{/if}
			</div>
		</Card>
	{/if}
</section>
