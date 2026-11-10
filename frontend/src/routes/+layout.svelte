<script lang="ts">
	import '../app.css';
	import { onMount, tick } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { afterNavigate, goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { page } from '$app/state';
	import { Button, Card, Icon, LoadingIndicator } from 'm3-svelte';
	import iconMenu from '@ktibow/iconset-material-symbols/menu';
	import { authService, creditService, realtimeService } from '$lib/services';
	import CreditBalance from '$lib/components/credit/CreditBalance.svelte';
	import AppSidebar from '$lib/components/shell/AppSidebar.svelte';
	import { clearSubmittedOrders } from '$lib/services/order/creation';
	import SnackbarHost from '$lib/components/SnackbarHost.svelte';
	let previousUser: string | undefined;
	$effect(() => {
		if (authService.user?.id && authService.accessToken) return realtimeService.start();
	});
	$effect(() => {
		if (authService.user?.id) return creditService.start();
	});
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

	// Below this width the sidebar becomes an off-canvas drawer behind a menu button.
	const compact = new MediaQuery('max-width: 60rem');
	let drawerOpen = $state(false);
	let menuControl = $state<HTMLDivElement>();
	const showSidebar = $derived(Boolean(authService.user) && !authenticationRoute);

	async function closeDrawer() {
		if (!drawerOpen) return;
		drawerOpen = false;
		await tick();
		menuControl?.querySelector('button')?.focus();
	}

	afterNavigate(() => { drawerOpen = false; });

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

<svelte:window onkeydown={(event) => { if (event.key === 'Escape' && drawerOpen) closeDrawer(); }} />

<a class="skip-link" href="#main-content">Skip to main content</a>
<div class="app-shell" class:with-sidebar={showSidebar}>
	<div class="snackbar-region"><SnackbarHost /></div>
	{#if showSidebar}
		<AppSidebar
			drawer={compact.current}
			open={drawerOpen}
			{signingOut}
			onclose={closeDrawer}
			onsignout={() => void signOut()}
		/>
	{/if}
	<div class="app-body" inert={showSidebar && compact.current && drawerOpen}>
		{#if showSidebar && compact.current}
			<header class="top-bar">
				<div bind:this={menuControl}>
					<Button
						variant="text"
						iconType="full"
						aria-label="Open menu"
						aria-controls="app-sidebar"
						aria-expanded={drawerOpen}
						onclick={() => (drawerOpen = true)}
					>
						<Icon icon={iconMenu} />
					</Button>
				</div>
				<a class="brand" href="/" aria-label="Friend on Campus home">FoC</a>
				<CreditBalance />
			</header>
		{:else if !showSidebar}
			<header class="site-header">
				<a class="brand" href="/" aria-label="Friend on Campus home">FoC · Friend on Campus</a>
			</header>
		{/if}
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
</div>
