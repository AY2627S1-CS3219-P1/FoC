<script lang="ts">
	import { userAuthClient } from '$lib/api/connect';
	import { auth } from '$lib/state/auth.svelte';

	let error = $state<string | null>(null);

	async function logout() {
		error = null;
		try {
			await userAuthClient.logout({});
			auth.logout();
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Unable to log out';
		}
	}
</script>

{#if auth.isLoading}
	<span>Checking session…</span>
{:else if auth.isAuthenticated}
	<span>Signed in as {auth.user?.displayName}</span>
	<button onclick={logout}>Log out</button>
{:else}
	<a href="/login">Log in</a>
{/if}
{#if error}
	<span role="alert">{error}</span>
{/if}
