<script lang="ts">
	import { Button, Icon } from 'm3-svelte';
	import addPhotoIcon from '@ktibow/iconset-material-symbols/add-photo-alternate-outline-rounded';
	import ProfileAvatar from '$lib/components/profile/ProfileAvatar.svelte';
	import {
		ProfilePictureController,
		type ProfilePictureSnapshot
	} from '$lib/features/profile-picture/controller';
	import { profileService } from '$lib/services';
	import { IMAGE_CONTENT_TYPES } from '$lib/features/image-upload/limits';
	import type { UserProfile } from '$lib/services/profile/profileApi';

	let {
		profile,
		onchange
	}: {
		profile: UserProfile;
		onchange: (profile: UserProfile) => void;
	} = $props();

	let fileInput: HTMLInputElement | undefined = $state();
	let view = $state<ProfilePictureSnapshot>({ outcome: 'Idle', message: '', busy: false });
	const controller = new ProfilePictureController(profileService, (snapshot) => {
		view = snapshot;
	});

	$effect(() => () => controller.dispose());

	const actionLabel = $derived(
		view.outcome === 'Uploading'
			? 'Uploading…'
			: view.outcome === 'Saving'
				? 'Saving…'
				: profile.profilePictureUrl
					? 'Change photo'
					: 'Add photo'
	);

	async function upload(file: File | undefined): Promise<void> {
		if (!file) return;
		const updated = await controller.upload(file);
		if (updated) onchange(profileService.profile ?? updated);
	}

	async function remove(): Promise<void> {
		const updated = await controller.remove();
		if (updated) {
			onchange(profileService.profile ?? updated);
			fileInput?.focus();
		}
	}

	async function refresh(): Promise<void> {
		const updated = await profileService.refreshPicture();
		if (updated) onchange(profileService.profile ?? updated);
	}
</script>

<div class="picture-editor">
	<ProfileAvatar
		url={profile.profilePictureUrl}
		name={profile.displayName}
		size={72}
		expiresAt={profile.profilePictureExpiresAt}
		onerror={() => void refresh()}
	/>
	<div class="picture-actions">
		<input
			bind:this={fileInput}
			id="profile-picture-file"
			class="file-input"
			type="file"
			accept={IMAGE_CONTENT_TYPES.join(',')}
			aria-label="Choose profile picture"
			aria-describedby="profile-picture-help"
			tabindex="-1"
			disabled={view.busy}
			onchange={(event) => {
				const file = event.currentTarget.files?.[0];
				event.currentTarget.value = '';
				void upload(file);
			}}
		/>
		<Button
			type="button"
			variant="outlined"
			iconType="left"
			aria-describedby="profile-picture-help"
			disabled={view.busy}
			onclick={() => fileInput?.click()}
		>
			<Icon icon={addPhotoIcon} size={20} aria-hidden="true" />{actionLabel}
		</Button>
		{#if profile.profilePictureUrl}
			<Button type="button" variant="text" disabled={view.busy} onclick={() => void remove()}>
				Remove photo
			</Button>
		{/if}
	</div>
</div>
<p id="profile-picture-help" class="picture-help">
	JPEG, PNG, WebP, HEIC or HEIF, up to 10MB. The picture appears beside your name.
</p>
<div class="picture-status-region" role="status" aria-live="polite">
	{#if view.message}
		<p class="picture-status" class:error={view.outcome === 'Failed'}>{view.message}</p>
	{/if}
</div>

<style>
	.picture-editor {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 1rem;
	}
	.picture-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
	}
	.file-input {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}
	.picture-help {
		margin: 0.5rem 0 0;
		color: var(--m3c-on-surface-variant);
		font-size: 0.875rem;
	}
	.picture-status-region {
		margin-top: 0.5rem;
	}
	.picture-status {
		margin: 0;
		color: var(--m3c-on-surface-variant);
		font-size: 0.875rem;
	}
	.picture-status.error {
		color: var(--m3c-error);
	}
</style>
