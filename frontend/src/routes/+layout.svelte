<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { authService } from '$lib/services';

	let { children } = $props();
	let status = $state<'loading' | 'ready' | 'error'>('loading');

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
	</header>

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
