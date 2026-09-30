<script lang="ts">
	import { goto } from '$app/navigation';
	import { Button, Card } from 'm3-svelte';
	import { authService } from '$lib/services';

	let isSigningOut = $state(false);
	let error = $state('');

	$effect(() => {
		if (!authService.isAuthenticated) {
			void goto('/login', { replaceState: true });
		}
	});

	async function signOut() {
		if (isSigningOut) return;
		isSigningOut = true;
		error = '';
		try {
			await authService.logout();
			await goto('/login', { replaceState: true });
		} catch {
			error = 'Could not sign out. Please try again.';
		} finally {
			isSigningOut = false;
		}
	}
</script>

{#if authService.user}
	<section class="center-stage" aria-label="Home">
		<div class="auth-panel">
			<Card variant="outlined">
				<div class="home-body">
					<h1>Welcome, {authService.user.displayName}</h1>
					<p>You're signed in as {authService.user.email}.</p>
					<p>Your campus home is coming soon.</p>
					{#if error}
						<p class="notice error" role="alert">{error}</p>
					{/if}
					<div class="form-actions">
						<Button variant="outlined" disabled={isSigningOut} onclick={() => void signOut()}>
							{isSigningOut ? 'Signing out…' : 'Sign out'}
						</Button>
					</div>
				</div>
			</Card>
		</div>
	</section>
{/if}
