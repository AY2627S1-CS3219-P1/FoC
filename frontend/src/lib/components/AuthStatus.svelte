<script lang="ts">
	import { authService } from '$lib/auth/auth-service.svelte';

	let error = $state<string | null>(null);

	async function logout() {
		error = null;
		try {
			await authService.logout();
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Unable to log out';
		}
	}
</script>

{#if authService.isLoading}
	<span>Checking session…</span>
{:else if authService.isAuthenticated}
	<span>Signed in as {authService.user?.displayName}</span>
	<button onclick={logout}>Log out</button>
{:else}
	<a href="/login">Log in</a>
{/if}
{#if error}
	<span role="alert">{error}</span>
{/if}
