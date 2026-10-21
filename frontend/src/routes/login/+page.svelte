<script lang="ts">
	import { onDestroy } from 'svelte';
	import { afterNavigate, goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, LoadingIndicator, TextFieldOutlined } from 'm3-svelte';
	import { authService } from '$lib/services';

	let email = $state('');
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

	async function sendLink(event: SubmitEvent) {
		event.preventDefault();
		if (sending) return;
		sending = true;
		error = '';
		try {
			await authService.requestLink(email.trim());
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

<section class="center-stage" aria-label="Sign in or create an account">
	<div class="auth-panel">
		<Card variant="outlined">
			<div class="auth-body">
				{#if redeeming}
					<div class="loading-state" role="status" aria-live="polite">
						<LoadingIndicator aria-label="Signing you in" />
						<h1>Signing you in…</h1>
					</div>
				{:else if sent}
					<h1>Check your email</h1>
					<p>We sent a sign-in link to {email.trim()}. Open it to continue.</p>
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
					<form class="auth-form" onsubmit={sendLink}>
						<TextFieldOutlined
							label="Email address"
							type="email"
							name="email"
							autocomplete="email"
							required
							bind:value={email}
							disabled={!mounted || sending}
						/>
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
