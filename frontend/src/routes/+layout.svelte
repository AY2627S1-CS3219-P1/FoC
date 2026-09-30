<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { page } from '$app/state';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { Button, Card, LoadingIndicator, TabsLink } from 'm3-svelte';
	import { authService } from '$lib/services';

	let { children } = $props();
	let status = $state<'loading' | 'ready' | 'error'>('loading');
	let signingOut = $state(false);
	let signOutError = $state('');

	async function restoreAuth() {
		status = 'loading';
		try {
			await authService.refresh();
			status = 'ready';
		} catch (error) {
			status =
				error instanceof ConnectError && error.code === Code.Unauthenticated ? 'ready' : 'error';
		}
	}

	async function signOut() {
		if (signingOut) return;
		signingOut = true;
		signOutError = '';
		try {
			await authService.logout();
			await goto('/login', { replaceState: true });
		} catch {
			signOutError = 'Could not sign out. Please try again.';
		} finally {
			signingOut = false;
		}
	}

	onMount(() => {
		void restoreAuth();
	});
</script>

<svelte:head>
	<title>Friend on Campus</title>
	<meta
		name="description"
		content="Find campus suppliers and connect with students who can help with errands."
	/>
</svelte:head>

<div class="app-shell">
	<header class="site-header">
		<a class="brand" href="/" aria-label="Friend on Campus home">FoC · Friend on Campus</a>
		{#if authService.user}
			<nav class="site-nav" aria-label="Account navigation">
				<TabsLink
					tab={page.url.pathname.startsWith('/admin') ? 'admin' : page.url.pathname.startsWith('/profile') ? 'profile' : 'home'}
					items={[
						{ name: 'Home', value: 'home', href: '/' },
						{ name: 'Profile', value: 'profile', href: '/profile' },
						...(authService.user.role === UserRole.SUPER_ADMIN || authService.user.role === UserRole.ADMIN
							? [{ name: 'Manage users', value: 'admin', href: '/admin/users' }]
							: [])
					]}
				/>
				<Button variant="text" disabled={signingOut} onclick={() => void signOut()}>{signingOut ? 'Signing out…' : 'Sign out'}</Button>
			</nav>
		{/if}
	</header>
	{#if signOutError}<p class="notice error" role="alert">{signOutError}</p>{/if}

	<main id="main-content" class="page-main">
		{#if status === 'loading'}
			<div class="center-stage">
				<div class="loading-state" role="status" aria-live="polite">
					<LoadingIndicator aria-label="Checking your session" />
					<p>Checking your session…</p>
				</div>
			</div>
		{:else if status === 'error'}
			<div class="center-stage">
				<div class="auth-panel">
					<Card variant="outlined">
						<div class="auth-body">
							<h1>We couldn't check your session</h1>
							<p>There may be a temporary connection problem. Please try again.</p>
							<div class="form-actions">
								<Button onclick={() => void restoreAuth()}>Try again</Button>
							</div>
						</div>
					</Card>
				</div>
			</div>
		{:else}
			{@render children()}
		{/if}
	</main>
</div>
