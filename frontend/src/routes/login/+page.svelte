<script lang="ts">
	import { onMount } from 'svelte';
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, LoadingIndicator, TextFieldOutlined } from 'm3-svelte';
	import { authService } from '$lib/services';

	let email = $state('');
	let sending = $state(false);
	let sent = $state(false);
	let redeeming = $state(page.url.searchParams.has('token'));
	let error = $state('');

	onMount(() => {
		const token = page.url.searchParams.get('token');
		if (token) {
			replaceState('/login', page.state);
			void redeemLink(token);
		} else if (authService.isAuthenticated) {
			void goto('/', { replaceState: true });
		}
	});

	async function redeemLink(token: string) {
		redeeming = true;
		try {
			await authService.login(token);
			await goto('/', { replaceState: true });
		} catch (cause) {
			error =
				cause instanceof ConnectError && cause.code === Code.Unauthenticated
					? 'This sign-in link has expired or has already been used. Request a new one below.'
					: 'We could not complete sign-in. Please request another link.';
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
		} catch {
			error = 'We could not send a link. Check your email address and try again.';
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
					<form class="auth-form" onsubmit={sendLink}>
						<TextFieldOutlined
							label="Email address"
							type="email"
							name="email"
							autocomplete="email"
							required
							bind:value={email}
							disabled={sending}
						/>
						<div class="form-actions">
							<Button type="submit" disabled={sending}>
								{sending ? 'Sending…' : 'Send me a sign-in link'}
							</Button>
						</div>
					</form>
				{/if}
			</div>
		</Card>
	</div>
</section>
