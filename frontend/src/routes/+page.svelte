<script lang="ts">
	import { goto } from '$app/navigation';
	import { Card } from 'm3-svelte';
	import { authService } from '$lib/services';

	$effect(() => {
		if (!authService.isAuthenticated) {
			void goto('/login', { replaceState: true });
		}
	});
</script>

{#if authService.user}
	<section class="center-stage" aria-label="Home">
		<div class="auth-panel">
			<Card variant="outlined">
				<div class="home-body">
					<h1>Welcome, {authService.user.displayName}</h1>
					<p>You're signed in as {authService.user.email}.</p>
					<p>Your campus home is coming soon.</p>
				</div>
			</Card>
		</div>
	</section>
{/if}
