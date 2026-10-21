<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { page } from '$app/state';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService } from '$lib/services';
	import { clearSubmittedOrders } from '$lib/services/order/creation';
	let previousUser: string | undefined;
	$effect(() => {
		const currentUser = authService.user?.id;
		if (previousUser && previousUser !== currentUser) {
			try { clearSubmittedOrders(sessionStorage); } catch { /* Storage may be disabled. */ }
		}
		previousUser = currentUser;
	});

	let { children } = $props();
	let status = $state<'loading' | 'ready' | 'error'>('loading');
	let signingOut = $state(false);
	let signOutError = $state('');
	let sessionError = $state('');
	$effect(() => {
		if (authService.isAuthenticated) sessionError = '';
	});
	const authenticationRoute = $derived(
		page.url.pathname === '/login' || page.url.pathname === '/register'
	);
	const canRender = $derived(
		authenticationRoute || status === 'ready' || authService.isAuthenticated
	);

	async function restoreAuth() {
		status = 'loading';
		sessionError = '';
		try {
			await authService.refresh();
			status = 'ready';
		} catch (error) {
			if (error instanceof ConnectError && error.code === Code.PermissionDenied) {
				sessionError = 'Your email domain is not allowed. Contact an administrator to restore access, or sign in with another email address.';
				status = 'ready';
				return;
			}
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
		// A magic link establishes its own session; do not race it with cookie refresh.
		if (authenticationRoute && page.url.searchParams.has('token')) status = 'ready';
		else void restoreAuth();
	});
</script>

<svelte:head>
	<title>Friend on Campus</title>
	<meta
		name="description"
		content="Find campus suppliers and connect with students who can help with errands."
	/>
</svelte:head>

<a class="skip-link" href="#main-content">Skip to main content</a>
<div class="app-shell">
	<header class="site-header">
		<a class="brand" href="/" aria-label="Friend on Campus home">FoC · Friend on Campus</a>
		{#if authService.user}
			<nav class="site-nav" aria-label="Account navigation">
				<div class="primary-links">
					{#each [{ name: 'Home', href: '/' }, { name: 'Locations', href: '/suppliers' }, { name: 'Orders', href: '/orders/mine' }, { name: 'Profile', href: '/profile' }, ...(authService.user.role === UserRole.SUPER_ADMIN || authService.user.role === UserRole.ADMIN ? [{ name: 'Administration', href: '/admin' }] : [])] as item}
						<Button
							variant="text"
							href={item.href}
							aria-current={(
								item.href === '/'
									? page.url.pathname === '/'
									: page.url.pathname.startsWith(item.href)
							)
								? 'page'
								: undefined}>{item.name}</Button
						>
					{/each}
				</div>
				<Button variant="text" disabled={signingOut} onclick={() => void signOut()}
					>{signingOut ? 'Signing out…' : 'Sign out'}</Button
				>
			</nav>
		{/if}
	</header>
	{#if signOutError}<p class="notice error" role="alert">{signOutError}</p>{/if}
	{#if sessionError}<p class="notice error" role="alert">{sessionError}</p>{/if}

	<main id="main-content" class="page-main" tabindex="-1">
		{#if canRender}
			{@render children()}
		{:else if status === 'loading'}
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
								<Button variant="text" href="/login">Go to sign in</Button>
								<Button onclick={() => void restoreAuth()}>Try again</Button>
							</div>
						</div>
					</Card>
				</div>
			</div>
		{/if}
	</main>
</div>
