<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import {
		Button,
		Card,
		Dialog,
		LoadingIndicator,
		TextFieldOutlined,
		TextFieldOutlinedMultiline
	} from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, profileService } from '$lib/services';
	import type { AccountDeletionStatus, UserProfile } from '$lib/services/profile/profileApi';
	import { profileSchema, type ProfileFormData } from '$lib/services/profile/profileSchema';
	import { reasonText } from '$lib/services/profile/deletionReasons';
	import { roleLabel } from '$lib/services/admin/roles';
	import PhoneField from '$lib/components/PhoneField.svelte';
	import TelegramLinkCard from '$lib/components/telegram/TelegramLinkCard.svelte';

	let profile = $state<UserProfile | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	let confirmOpen = $state(false);
	let deleting = $state(false);
	let deleteError = $state('');

	let deletionStatus = $state<AccountDeletionStatus | null>(null);
	let deletionCheckError = $state(false);

	// The session's lifecycle flag drives every restricted control; the server
	// stays authoritative if this UI state is stale.
	const isDeleting = $derived(authService.user?.isDeleting === true);

	function valuesFromProfile(value: UserProfile): ProfileFormData {
		return {
			displayName: value.displayName,
			description: value.description,
			phoneNumber: value.phoneNumber ?? ''
		};
	}

	const { form, errors, constraints, message, submitting, enhance, reset, isTainted, tainted } =
		superForm(defaults(zod4(profileSchema)), {
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
						phoneNumber: validated.data.phoneNumber
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
		});

	function errorMessage(cause: unknown): string {
		if (cause instanceof ConnectError) {
			if (cause.code === Code.InvalidArgument)
				return 'The profile details were rejected. Check the fields and try again.';
			if (cause.code === Code.PermissionDenied)
				return 'Your account cannot edit its profile. If you have requested account deletion, this cannot be undone.';
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

	async function acceptDeletion() {
		deleting = true;
		deleteError = '';
		try {
			// Applies the restricted replacement session through the session
			// revision, so an in-flight refresh cannot restore unrestricted state.
			await authService.deleteMyAccount();
			confirmOpen = false;
			await loadProfile();
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.Unauthenticated) {
				confirmOpen = false;
				await goto('/login', { replaceState: true });
				return;
			}
			deleteError = cause instanceof ConnectError && cause.code === Code.FailedPrecondition
				? 'This account cannot be deleted. Protected accounts must demote first.'
				: 'We could not record your request. Nothing has been deleted; please try again.';
		} finally {
			deleting = false;
		}
	}

	// Poll the deletion worker's status with a backoff that grows to a minute.
	// A failed check is reported as unavailable and retried, never as empty.
	$effect(() => {
		if (!isDeleting) return;
		const identity = authService.user?.id;
		let stopped = false;
		let delay = 5000;
		let timer: ReturnType<typeof setTimeout> | undefined;

		async function check() {
			if (stopped || authService.user?.id !== identity) return;
			try {
				const status = await profileService.getMyAccountDeletionStatus();
				if (stopped) return;
				deletionStatus = status;
				deletionCheckError = false;
				delay = 5000;
			} catch {
				if (stopped) return;
				deletionCheckError = true;
				delay = Math.min(delay * 2, 60000);
			}
			timer = setTimeout(() => void check(), delay);
		}

		void check();
		return () => {
			stopped = true;
			if (timer) clearTimeout(timer);
		};
	});

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
		<div>
			<p class="eyebrow">Your account</p>
			<h1 id="profile-title">Your profile</h1>
		</div>
		{#if profile}<span class="role-chip">{roleLabel(profile.role)}</span>{/if}
	</header>

	{#if loading}
		<div class="loading-state" role="status" aria-live="polite">
			<LoadingIndicator aria-label="Loading profile" />
			<p>Loading your profile…</p>
		</div>
	{:else if loadError}
		<Card variant="outlined"
			><div class="panel-body">
				<p class="notice error" role="alert">{loadError}</p>
				<div class="form-actions">
					<Button onclick={() => void loadProfile()}>Try again</Button>
				</div>
			</div></Card
		>
	{:else if profile}
		{#if isDeleting}
			<Card variant="outlined">
				<div class="panel-body">
					<p class="eyebrow">Account deletion</p>
					<h2 class="deletion-title">Deletion requested</h2>
					<p class="notice" role="status">
						Account deletion requested. Finish your existing orders and withdraw outstanding
						offers. You cannot start new work or cancel this request. Your reviews and order
						history stay visible to others without your name; your photos will be removed. Leftover
						credits are not transferred.
					</p>
					{#if deletionCheckError}
						<p class="notice" role="status">
							Checking unavailable, will retry. We will keep checking automatically.
						</p>
					{:else if deletionStatus}
						{#if deletionStatus.waitingReasons.length > 0}
							<p class="deletion-heading">What is still waiting:</p>
							<ul class="deletion-reasons">
								{#each deletionStatus.waitingReasons as reason (reason)}
									<li>{reasonText(reason)}</li>
								{/each}
							</ul>
						{:else}
							<p class="notice" role="status">
								No blockers are currently reported. Your account will be removed automatically
								when the final checks pass.
							</p>
						{/if}
						{#if deletionStatus.lastCheckedAt}
							<p class="deletion-checked">
								Last checked {deletionStatus.lastCheckedAt.toLocaleTimeString()}.
							</p>
						{/if}
					{/if}
				</div>
			</Card>
		{/if}
		<Card variant="outlined">
			<div class="panel-body">
				<div class="account-facts">
					<div><span>Email</span><strong>{profile.email}</strong></div>
					<div><span>Account role</span><strong>{roleLabel(profile.role)}</strong></div>
				</div>
				{#if profile.role === UserRole.SUSPENDED_USER || isDeleting}
					<p class="notice" role="status">
						{isDeleting
							? 'Your account is being deleted. You can view your details, but editing is unavailable.'
							: 'Your account is suspended. You can view your profile, but editing is unavailable.'}
					</p>
					<div class="account-facts">
						<div><span>Display name</span><strong>{profile.displayName}</strong></div>
						<div><span>Description</span><strong>{profile.description || '—'}</strong></div>
						<div><span>Phone</span><strong>{profile.phoneNumber || '—'}</strong></div>
					</div>
				{:else}
					{#if $message}<p class="notice" role="status" aria-live="polite">{$message}</p>{/if}
					<form class="data-form" method="POST" use:enhance>
						<div class="field">
							<TextFieldOutlined
								label="Display name"
								name="displayName"
								autocomplete="name"
								required
								bind:value={$form.displayName}
								maxlength={Number($constraints.displayName?.maxlength ?? 100)}
								error={!!$errors.displayName}
								aria-invalid={$errors.displayName ? 'true' : undefined}
								aria-describedby={$errors.displayName ? 'profile-displayName-error' : undefined}
							/>{#if $errors.displayName}<small class="field-error" id="profile-displayName-error"
									>{$errors.displayName[0]}</small
								>{/if}
						</div>
						<div class="field">
							<TextFieldOutlinedMultiline
								label="Description"
								name="description"
								rows={5}
								bind:value={$form.description}
								maxlength={Number($constraints.description?.maxlength ?? 500)}
								error={!!$errors.description}
								aria-invalid={$errors.description ? 'true' : undefined}
								aria-describedby={$errors.description ? 'profile-description-error' : undefined}
							/><small class="field-meta">{Array.from($form.description).length}/500</small
							>{#if $errors.description}<small class="field-error" id="profile-description-error"
									>{$errors.description[0]}</small
								>{/if}
						</div>
						<div class="field">
							<PhoneField
								id="profile-phoneNumber"
								required
								bind:value={$form.phoneNumber}
								error={$errors.phoneNumber?.[0] ?? ''}
							/>
						</div>
						<div class="form-actions">
							<Button
								type="button"
								variant="outlined"
								disabled={$submitting || !isTainted($tainted)}
								onclick={() => reset()}>Cancel</Button
							><Button
								type="submit"
								disabled={$submitting ||
									!isTainted($tainted) ||
									Object.values($errors).some((value) => value?.length)}
								>{$submitting ? 'Saving…' : 'Save changes'}</Button
							>
						</div>
					</form>
				{/if}
			</div>
		</Card>
		{#if !isDeleting}
			<Card variant="outlined">
				<div class="panel-body">
					<div><p class="eyebrow">Notifications</p><h2 class="telegram-title">Telegram</h2></div>
					<TelegramLinkCard />
				</div>
			</Card>
		{/if}
	{/if}
</section>

<Dialog
	bind:open={confirmOpen}
	headline="Delete your account?"
	aria-label="Delete your account?"
	closedby={deleting ? 'none' : 'any'}
	oncancel={(event) => {
		if (deleting) event.preventDefault();
	}}
>
	<div class="confirm-body">
		<p>This permanently deletes your account. It cannot be undone and the request cannot be
			cancelled later.</p>
		<ul>
			<li>Your reviews and order history stay visible to others without your name.</li>
			<li>Photos you own are removed.</li>
			<li>You will not be able to sign in after your existing work is finished.</li>
			<li>Leftover credits are not transferred.</li>
		</ul>
		{#if deleteError}<p class="notice error" role="alert">{deleteError}</p>{/if}
	</div>
	{#snippet buttons()}
		<Button type="button" variant="text" disabled={deleting} onclick={() => (confirmOpen = false)}>
			Keep my account
		</Button>
		<Button
			type="button"
			variant="filled"
			class="delete-confirm"
			disabled={deleting}
			onclick={() => void acceptDeletion()}>
			{deleting ? 'Deleting…' : 'Delete my account forever'}
		</Button>
	{/snippet}
</Dialog>

{#if profile && !isDeleting}
	<a
		class="delete-entry"
		href="#delete-account"
		onclick={(event) => {
			event.preventDefault();
			deleteError = '';
			confirmOpen = true;
		}}>Delete account…</a
	>
{/if}

<style>
	.deletion-title {
		margin: 0.1rem 0 0.5rem;
		font-size: 1.25rem;
	}
	.deletion-heading {
		margin: 0.25rem 0 0;
		font-weight: 600;
	}
	.deletion-reasons {
		margin: 0.25rem 0 0;
		padding-left: 1.25rem;
	}
	.deletion-checked {
		margin: 0.5rem 0 0;
		font-size: 0.85rem;
		opacity: 0.75;
	}
	.confirm-body p {
		margin: 0 0 0.5rem;
	}
	.confirm-body ul {
		margin: 0;
		padding-left: 1.25rem;
	}
	/* Applied through the m3-svelte Button component, so Svelte cannot see it. */
	:global(.delete-confirm) {
		background: var(--m3c-error);
		color: var(--m3c-on-error, #fff);
	}
	.delete-entry {
		justify-self: start;
		margin: 1rem 0 2rem;
		color: var(--m3c-error);
		font-size: 0.9rem;
	}
</style>
