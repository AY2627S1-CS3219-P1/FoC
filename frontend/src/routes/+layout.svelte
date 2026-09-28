<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import AuthStatus from '$lib/components/AuthStatus.svelte';
	import { authService } from '$lib/auth/auth-service.svelte';

	let { children } = $props();

	onMount(() => {
		void authService.restoreSession();
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
		<a class="brand" href="/" aria-label="Friend on Campus home">Friend on Campus</a>
		<nav aria-label="Main navigation">
			<a href="/suppliers">Suppliers</a>
			<AuthStatus />
		</nav>
	</header>

	<main id="main-content">
		{@render children()}
	</main>
</div>
