<script lang="ts">
	import { Icon, Menu, MenuItem } from 'm3-svelte';
	import addPhotoIcon from '@ktibow/iconset-material-symbols/add-photo-alternate-outline-rounded';
	import editIcon from '@ktibow/iconset-material-symbols/edit-rounded';
	import deleteIcon from '@ktibow/iconset-material-symbols/delete-outline-rounded';
	import ProfileAvatar from '$lib/components/profile/ProfileAvatar.svelte';
	import ConfirmDialog from '$lib/components/admin/ConfirmDialog.svelte';
	import {
		ProfilePictureController,
		type ProfilePictureSnapshot
	} from '$lib/features/profile-picture/controller';
	import { profileService, snackbarService } from '$lib/services';
	import { IMAGE_CONTENT_TYPES } from '$lib/features/image-upload/limits';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import type { UserProfile } from '$lib/services/profile/profileApi';

	let {
		profile,
		onchange
	}: {
		profile: UserProfile;
		onchange: (profile: UserProfile) => void;
	} = $props();

	let fileInput: HTMLInputElement | undefined = $state();
	let picture: HTMLDivElement | undefined = $state();
	let view = $state<ProfilePictureSnapshot>({ outcome: 'Idle', message: '', busy: false });
	let menuOpen = $state(false);
	let confirmOpen = $state(false);

	const controller = new ProfilePictureController(profileService, (snapshot) => {
		view = snapshot;
	});
	$effect(() => () => controller.dispose());

	const suspended = $derived(profile.role === UserRole.SUSPENDED_USER);
	const hasPicture = $derived(Boolean(profile.profilePictureUrl));

	// Terminal outcomes reach the snackbar; progress stays on the button state.
	let lastMessage = '';
	$effect(() => {
		const message = view.message;
		if (!message) {
			lastMessage = '';
			return;
		}
		if (message === lastMessage) return;
		lastMessage = message;
		if (view.outcome === 'Succeeded') {
			snackbarService.show(message, { severity: 'success' });
		} else if (view.outcome === 'Failed') {
			snackbarService.show(message, { severity: 'error', timeout: 8000 });
		}
	});

	async function upload(file: File | undefined): Promise<void> {
		if (!file) return;
		menuOpen = false;
		const updated = await controller.upload(file);
		if (updated) onchange(profileService.profile ?? updated);
	}

	async function remove(): Promise<void> {
		const updated = await controller.remove();
		if (updated) onchange(profileService.profile ?? updated);
		confirmOpen = false;
	}

	async function refresh(): Promise<void> {
		const updated = await profileService.refreshPicture();
		if (updated) onchange(profileService.profile ?? updated);
	}

	function pickFile(): void {
		menuOpen = false;
		fileInput?.click();
	}

	function onWindowClick(event: MouseEvent): void {
		if (!menuOpen || !picture) return;
		if (!picture.contains(event.target as Node)) menuOpen = false;
	}
</script>

<svelte:window onclick={onWindowClick} onkeydown={(event) => event.key === 'Escape' && (menuOpen = false)} />

<div class="picture" bind:this={picture}>
	<ProfileAvatar
		url={profile.profilePictureUrl}
		name={profile.displayName}
		size={72}
		expiresAt={profile.profilePictureExpiresAt}
		onerror={() => void refresh()}
	/>
	{#if !suspended}
		<input
			bind:this={fileInput}
			id="profile-picture-file"
			class="file-input"
			type="file"
			accept={IMAGE_CONTENT_TYPES.join(',')}
			aria-label="Choose profile picture"
			tabindex="-1"
			disabled={view.busy}
			onchange={(event) => {
				const file = event.currentTarget.files?.[0];
				event.currentTarget.value = '';
				void upload(file);
			}}
		/>
		<button
			type="button"
			class="picture-action"
			aria-label={hasPicture ? 'Edit profile picture' : 'Add profile picture'}
			aria-haspopup={hasPicture ? 'menu' : undefined}
			aria-expanded={hasPicture ? menuOpen : undefined}
			disabled={view.busy}
			onclick={() => (hasPicture ? (menuOpen = !menuOpen) : pickFile())}
		>
			<Icon icon={hasPicture ? editIcon : addPhotoIcon} size={18} aria-hidden="true" />
		</button>
		{#if hasPicture && menuOpen}
			<div class="menu-pop">
				<Menu>
					<MenuItem icon={editIcon} onclick={pickFile}>Edit</MenuItem>
					<MenuItem icon={deleteIcon} onclick={() => ((menuOpen = false), (confirmOpen = true))}
						>Remove</MenuItem
					>
				</Menu>
			</div>
		{/if}
	{/if}
</div>

<ConfirmDialog
	bind:open={confirmOpen}
	title="Remove profile picture?"
	label="Remove"
	busy={view.busy}
	onconfirm={() => void remove()}
>
	Your current picture will be deleted. This can't be undone.
</ConfirmDialog>

<style>
	.picture { position: relative; flex: 0 0 auto; display: inline-grid; }
	.picture-action {
		position: absolute;
		top: -0.4rem;
		right: -0.4rem;
		display: grid;
		place-items: center;
		width: 2rem;
		height: 2rem;
		padding: 0;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: 50%;
		background: var(--m3c-surface);
		color: var(--m3c-primary);
		cursor: pointer;
	}
	.picture-action::before { content: ''; position: absolute; inset: -0.4rem; border-radius: 50%; }
	.picture-action:focus-visible { outline: 3px solid var(--m3c-primary); outline-offset: 2px; }
	.picture-action:disabled { opacity: 0.5; cursor: default; }
	.menu-pop { position: absolute; top: calc(100% + 0.5rem); left: 0; z-index: 10; }
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
</style>
