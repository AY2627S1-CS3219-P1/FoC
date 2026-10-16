<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Button, LoadingIndicator } from 'm3-svelte';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { allowedDomainService, authService } from '$lib/services';
	import AllowedDomainsCard from './AllowedDomainsCard.svelte';

	let canView = $derived(authService.user?.role === UserRole.ADMIN || authService.user?.role === UserRole.SUPER_ADMIN);

	async function loadDomains(): Promise<void> {
		try {
			await allowedDomainService.listAllowedDomains();
		} catch {}
	}

	$effect(() => {
		if (authService.isAuthenticated && !canView) void goto('/', { replaceState: true });
	});

	onMount(() => {
		void loadDomains();
	});
</script>

<svelte:head><title>Config · Friend on Campus</title></svelte:head>

{#if canView}
	<section class="page-content admin-page" aria-labelledby="config-title">
		<header class="page-heading">
			<div><p class="eyebrow">Administration</p><h1 id="config-title">Config</h1><p>Manage application configuration.</p></div>
		</header>
		{#if !allowedDomainService.hasLoaded}
			{#if allowedDomainService.loadError}
				<div class="form-actions"><Button variant="outlined" onclick={() => void loadDomains()}>Try again</Button></div>
			{:else}
				<div class="loading-state" role="status" aria-live="polite"><LoadingIndicator aria-label="Loading allowed domains" /><p>Loading allowed domains…</p></div>
			{/if}
		{:else}
			<AllowedDomainsCard />
		{/if}
	</section>
{/if}
