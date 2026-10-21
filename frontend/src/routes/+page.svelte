<script lang="ts">
	import { goto } from '$app/navigation';
	import { Button, Card } from 'm3-svelte';
	import { authService } from '$lib/services';
	import { isAdministrator } from '$lib/services/location-admin/service.svelte';
	$effect(() => {
		if (!authService.isAuthenticated) void goto('/login', { replaceState: true });
	});
</script>

<svelte:head><title>Home · Friend on Campus</title></svelte:head>
{#if authService.user}
	<section class="page-content" aria-labelledby="home-title">
		<header class="page-heading">
			<div>
				<p class="eyebrow">Friend on Campus</p>
				<h1 id="home-title">Welcome, {authService.user.displayName}</h1>
				<p>Find campus locations and manage your account.</p>
			</div>
		</header>
		<Card variant="outlined"
			><div class="panel-body">
				<h2>Campus locations</h2>
				<p>Browse campus suppliers and facilities, opening hours, and directions.</p>
				<div class="form-actions"><Button href="/suppliers">Browse locations</Button></div>
			</div></Card
		>
		<Card variant="outlined"
			><div class="panel-body">
				<h2>Your profile</h2>
				<p>Review your account details and keep your contact information up to date.</p>
				<div class="form-actions">
					<Button variant="outlined" href="/profile">View profile</Button>
				</div>
			</div></Card
		>
		{#if isAdministrator(authService.user.role)}<Card variant="outlined"
				><div class="panel-body">
					<h2>Administration</h2>
					<p>Maintain locations, review addition requests, and manage user roles.</p>
					<div class="form-actions">
						<Button variant="outlined" href="/admin">Open administration</Button>
					</div>
				</div></Card
			>{/if}
	</section>
{/if}
