<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService } from '$lib/services';
	import AllowedDomainsCard from '$lib/components/admin/AllowedDomainsCard.svelte';

	let mounted = $state(false);
	let canView = $derived(authService.user?.role === UserRole.ADMIN || authService.user?.role === UserRole.SUPER_ADMIN);

	onMount(() => { mounted = true; });
	$effect(() => {
		if (!mounted) return;
		if (!authService.isAuthenticated) void goto('/login', { replaceState: true });
		else if (!canView) void goto('/', { replaceState: true });
	});
</script>

<svelte:head><title>Config · Friend on Campus</title></svelte:head>

{#if canView}
	<section class="page-content admin-page" aria-labelledby="config-title">
		<header class="page-heading">
			<div><p class="eyebrow">Administration</p><h1 id="config-title">Config</h1><p>Manage application configuration.</p></div>
		</header>
		<AllowedDomainsCard />
	</section>
{/if}
