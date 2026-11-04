<script lang="ts">
	import { onDestroy } from 'svelte';
	import { afterNavigate, goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, Checkbox, TextFieldOutlined } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { z } from 'zod';
	import { isValidPhoneNumber } from 'libphonenumber-js';
	import TelegramLinkCard from '$lib/components/telegram/TelegramLinkCard.svelte';
	import PhoneField from '$lib/components/PhoneField.svelte';
	import { authService } from '$lib/services';

	let token = $state<string | null>(page.url.searchParams.get('token'));
	let connectTelegram = $state(false);
	let registered = $state(false);
	const registrationSchema = z.object({
		displayName: z.string().trim().min(1, 'Enter a display name.')
			.refine((name) => Array.from(name).length <= 100, 'Use 100 characters or fewer.'),
		phoneNumber: z.string().trim().min(1, 'Enter a phone number.')
			.refine((phone) => isValidPhoneNumber(phone, 'SG'), 'Enter a valid phone number.')
	});
	const { form, errors, enhance } = superForm(defaults(zod4(registrationSchema)), {
		SPA: true,
		validators: zod4Client(registrationSchema),
		resetForm: false,
		multipleSubmits: 'prevent',
		async onUpdate({ form: validated }) {
			if (validated.valid) await register(validated.data.displayName, validated.data.phoneNumber);
		}
	});
	let submitting = $state(false);
	let error = $state('');
	let mounted = $state(false);
	let active = true;
	onDestroy(() => {
		active = false;
	});

	afterNavigate(async ({ complete }) => {
		// Initial afterNavigate callbacks run before the router marks itself started.
		await complete;
		if (!active) return;
		mounted = true;
		token = page.url.searchParams.get('token');
		if (token) {
			replaceState('/register', page.state);
		} else if (authService.isAuthenticated && !registered) {
			void goto('/', { replaceState: true });
		}
	});

	async function register(name: string, phone: string) {
		if (!token || submitting || registered) return;
		submitting = true;
		error = '';
		try {
			await authService.register(token, name, phone);
			registered = true;
			token = null;
			if (!connectTelegram) await goto('/', { replaceState: true });
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

<svelte:head><title>Create your profile · Friend on Campus</title></svelte:head>

<section class="center-stage" aria-label="Create your profile">
	<div class="auth-panel">
		<Card variant="outlined">
			<div class="auth-body">
				<h1>Create your profile</h1>
				{#if registered}
					<p role="status">Your profile is ready.</p>
					<h2>Telegram <small>(optional)</small></h2>
					<TelegramLinkCard autoConnect />
					<div class="form-actions">
						<Button onclick={() => void goto('/', { replaceState: true })}>Continue to home</Button>
					</div>
				{:else if token}
					<p>Choose the name other people on campus will see, and add a phone number as your backup contact.</p>
					{#if error}
						<p class="notice error" role="alert">{error}</p>
					{/if}
					<form class="auth-form" method="POST" use:enhance>
						<TextFieldOutlined
							label="Display name"
							type="text"
							name="displayName"
							autocomplete="name"
							maxlength={100}
							required
							bind:value={$form.displayName}
							error={!!$errors.displayName}
							aria-describedby={$errors.displayName ? 'register-name-error' : undefined}
							disabled={!mounted || submitting}
						/>
						{#if $errors.displayName}<small id="register-name-error" class="field-error">{$errors.displayName[0]}</small>{/if}
						<PhoneField required disabled={!mounted || submitting} bind:value={$form.phoneNumber} error={$errors.phoneNumber?.[0] ?? ''} />
						<label class="telegram-option">
							<Checkbox>
								<input type="checkbox" bind:checked={connectTelegram} disabled={!mounted || submitting} />
							</Checkbox>
							Connect Telegram (optional)
						</label>
						<div class="form-actions">
							<Button type="submit" disabled={!mounted || submitting}>
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

<style>
	.telegram-option { display: flex; align-items: center; gap: 0.75rem; }
	h2 { margin: 0; font-size: 1.25rem; }
	h2 small { font-size: 0.875rem; font-weight: 400; color: var(--m3c-on-surface-variant); }
</style>
