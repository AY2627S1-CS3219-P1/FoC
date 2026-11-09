<script lang="ts">
	import { onDestroy } from 'svelte';
	import { afterNavigate, goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, LoadingIndicator, TextFieldOutlined } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { z } from 'zod';
	import { authService } from '$lib/services';

	let sentEmail = $state('');
	const emailSchema = z.object({ email: z.string().trim().email('Enter a valid email address.') });
	const { form, errors, enhance } = superForm(defaults(zod4(emailSchema)), {
		SPA: true,
		validators: zod4Client(emailSchema),
		resetForm: false,
		multipleSubmits: 'prevent',
		async onUpdate({ form: validated }) {
			if (validated.valid) await sendLink(validated.data.email);
		}
	});
	let sending = $state(false);
	let sent = $state(false);
	let token = $state<string | null>(page.url.searchParams.get('token'));
	let redeeming = $state(!!page.url.searchParams.get('token'));
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
			replaceState('/login', page.state);
			void redeemLink();
		} else if (authService.isAuthenticated) {
			void goto('/', { replaceState: true });
		}
	});

	$effect(() => {
		if (mounted && authService.isAuthenticated && !redeeming)
			void goto('/', { replaceState: true });
	});

	async function redeemLink() {
		if (!token) return;
		redeeming = true;
		error = '';
		try {
			await authService.login(token);
			token = null;
			await goto('/', { replaceState: true });
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.Unauthenticated) {
				token = null;
				error = 'This sign-in link has expired or has already been used. Request a new one below.';
			} else if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
				error = 'Your email domain is not allowed. Contact an administrator, then retry before this link expires.';
			} else {
				error = 'We could not complete sign-in. Please try again.';
			}
		} finally {
			redeeming = false;
		}
	}

	async function sendLink(email: string) {
		if (sending) return;
		sending = true;
		error = '';
		try {
			await authService.requestLink(email);
			sentEmail = email;
			sent = true;
		} catch (cause) {
			error = cause instanceof ConnectError && cause.code === Code.PermissionDenied
				? 'Your email domain is not allowed. Contact an administrator to request access.'
				: 'We could not send a link. Check your email address and try again.';
		} finally {
			sending = false;
		}
	}
</script>

<svelte:head><title>Sign in · Friend on Campus</title></svelte:head>

<section class="center-stage" aria-label="Sign in or create an account">
	<div class="auth-panel">
		<Card variant="outlined">
			<div class="auth-body">
				<div class="login-brand"><span aria-hidden="true">FoC</span><p>Friend on Campus</p></div>
				{#if redeeming}
					<div class="loading-state" role="status" aria-live="polite">
						<LoadingIndicator aria-label="Signing you in" />
						<h1>Signing you in…</h1>
					</div>
				{:else if sent}
					<h1>Check your email</h1>
					<p>We sent a sign-in link to {sentEmail}. Open it to continue.</p>
					<p>New here? The link will take you through a short profile step.</p>
					<div class="form-actions">
						<Button variant="text" onclick={() => (sent = false)}>Use another email</Button>
					</div>
				{:else}
					<h1>Sign in or create an account</h1>
					<p>Enter your email address and we'll send you a secure sign-in link.</p>
					{#if error}
						<p class="notice error" role="alert">{error}</p>
					{/if}
					{#if token}
						<div class="form-actions">
							<Button onclick={() => void redeemLink()}>Retry sign-in link</Button>
						</div>
					{/if}
					<form class="auth-form" method="POST" use:enhance>
						<TextFieldOutlined
							label="Email address"
							type="email"
							name="email"
							autocomplete="email"
							required
							bind:value={$form.email}
							error={!!$errors.email}
							aria-describedby={$errors.email ? 'login-email-error' : undefined}
							disabled={!mounted || sending}
						/>
						{#if $errors.email}<small id="login-email-error" class="field-error">{$errors.email[0]}</small>{/if}
						<div class="form-actions">
							<Button type="submit" disabled={!mounted || sending}>
								{sending ? 'Sending…' : 'Send me a sign-in link'}
							</Button>
						</div>
					</form>
				{/if}
			</div>
		</Card>
	</div>
</section>

<style>
	.login-brand { display: grid; justify-items: center; gap: 0.5rem; padding-block: 0.75rem; }
	.login-brand span { color: var(--m3c-primary); font-size: 3.5rem; font-weight: 800; letter-spacing: -0.03em; line-height: 1; }
</style>
