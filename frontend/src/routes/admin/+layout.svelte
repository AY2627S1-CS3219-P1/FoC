<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button } from 'm3-svelte';
	import { authService, supplierAdminAccess } from '$lib/services';
	import { isAdministrator } from '$lib/services/location-admin/service.svelte';
	let { children } = $props();
	let refreshing = $state(false);
	let error = $state('');
	const allowed = $derived(isAdministrator(authService.user?.role));
	$effect(() => {
		if (!authService.isAuthenticated) void goto('/login', { replaceState: true });
	});
	async function recover() {
		refreshing = true;
		error = '';
		try {
			await supplierAdminAccess.recover();
		} catch {
			error = 'Could not refresh your session. Please try again.';
		} finally {
			refreshing = false;
		}
	}
</script>

{#if !allowed || supplierAdminAccess.denied}
	<section class="page-content">
		<h1>Administrator access required</h1>
		<p class="notice" role="status">
			Your account cannot currently access administration. Refresh your session if your role has
			changed.
		</p>
		{#if error}<p class="notice error" role="alert">{error}</p>{/if}
		<div class="form-actions">
			<Button variant="text" href="/">Home</Button><Button disabled={refreshing} onclick={recover}
				>{refreshing ? 'Refreshing…' : 'Refresh session'}</Button
			>
		</div>
	</section>
{:else}
	<div class="admin-workspace">
		<nav class="admin-navigation" aria-label="Administration">
			{#each [{ href: '/admin/suppliers', label: 'Locations' }, { href: '/admin/supplier-requests', label: 'Addition requests' }, { href: '/admin/users', label: 'Users' }] as item}
				<a
					href={item.href}
					aria-current={page.url.pathname.startsWith(item.href) ? 'page' : undefined}
					>{item.label}</a
				>
			{/each}
		</nav>
		{@render children()}
	</div>
{/if}
