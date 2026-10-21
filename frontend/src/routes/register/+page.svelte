<script lang="ts">
	import { onMount } from 'svelte';
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, TextFieldOutlined } from 'm3-svelte';
	import { isValidPhoneNumber } from 'libphonenumber-js';
	import PhoneField from '$lib/components/PhoneField.svelte';
	import { authService } from '$lib/services';

	let token = $state<string | null>(page.url.searchParams.get('token'));
	let displayName = $state('');
	let phoneNumber = $state('');
	let submitting = $state(false);
	let error = $state('');

	onMount(() => {
		token = page.url.searchParams.get('token');
		if (token) {
			replaceState('/register', page.state);
		} else if (authService.isAuthenticated) {
			void goto('/', { replaceState: true });
		}
	});

	async function register(event: SubmitEvent) {
		event.preventDefault();
		if (!token || submitting) return;
		const name = displayName.trim();
		if (Array.from(name).length === 0 || Array.from(name).length > 100) {
			error = 'Enter a display name of up to 100 characters.';
			return;
		}
		const phone = phoneNumber.trim();
		if (!phone || !isValidPhoneNumber(phone, 'SG')) {
			error = 'Enter a valid phone number.';
			return;
		}
		submitting = true;
		error = '';
		try {
			await authService.register(token, name, phone);
			await goto('/welcome/telegram', { replaceState: true });
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.Unauthenticated) {
				token = null;
				error = 'This registration link has expired or has already been used.';
			} else if (cause instanceof ConnectError && cause.code === Code.AlreadyExists) {
				token = null;
				error = 'This account already exists. Request a new sign-in link.';
			} else if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
				error = 'Your email domain is not allowed. Contact an administrator, then retry before this link expires.';
			} else {
				error = 'We could not create your profile. Please try again.';
			}
		} finally {
			submitting = false;
		}
	}
</script>

<section class="center-stage" aria-label="Create your profile">
	<div class="auth-panel">
		<Card variant="outlined">
			<div class="auth-body">
				<h1>Create your profile</h1>
				{#if token}
					<p>Choose the name other people on campus will see, and add a phone number as your backup contact.</p>
					{#if error}
						<p class="notice error" role="alert">{error}</p>
					{/if}
					<form class="auth-form" onsubmit={register}>
						<TextFieldOutlined
							label="Display name"
							type="text"
							name="displayName"
							autocomplete="name"
				maxlength={100}
							required
							bind:value={displayName}
							disabled={submitting}
						/>
						<PhoneField required disabled={submitting} bind:value={phoneNumber} />
						<div class="form-actions">
							<Button type="submit" disabled={submitting}>
								{submitting ? 'Creating profile…' : 'Complete sign-up'}
							</Button>
						</div>
					</form>
				{:else}
					<p class="notice error" role="alert">
						{error || 'Open the registration link from your email to continue.'}
					</p>
					<div class="form-actions">
						<Button href="/login">Request another link</Button>
					</div>
				{/if}
			</div>
		</Card>
	</div>
</section>
