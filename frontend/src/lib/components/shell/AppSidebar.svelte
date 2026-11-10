<script lang="ts">
	import { tick } from 'svelte';
	import { page } from '$app/state';
	import { Button, Icon } from 'm3-svelte';
	import iconAdd from '@ktibow/iconset-material-symbols/add';
	import iconAdmin from '@ktibow/iconset-material-symbols/admin-panel-settings-outline';
	import iconClose from '@ktibow/iconset-material-symbols/close';
	import iconDeliver from '@ktibow/iconset-material-symbols/local-shipping-outline';
	import iconHistory from '@ktibow/iconset-material-symbols/receipt-long-outline';
	import iconHome from '@ktibow/iconset-material-symbols/home';
	import iconLogout from '@ktibow/iconset-material-symbols/logout';
	import iconProfile from '@ktibow/iconset-material-symbols/person-outline';
	import iconSuppliers from '@ktibow/iconset-material-symbols/storefront';
	import type { IconifyIcon } from '@iconify/types';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { authService, profileService } from '$lib/services';
	import CreditBalance from '$lib/components/credit/CreditBalance.svelte';
	import ProfileAvatar from '$lib/components/profile/ProfileAvatar.svelte';
	import OngoingRequests from './OngoingRequests.svelte';

	let {
		drawer = false,
		open = false,
		signingOut = false,
		onclose,
		onsignout
	}: {
		/** True when the sidebar is an off-canvas drawer rather than a column. */
		drawer?: boolean;
		open?: boolean;
		signingOut?: boolean;
		onclose: () => void;
		onsignout: () => void;
	} = $props();

	let sidebar: HTMLElement;
	$effect(() => {
		if (drawer && open) {
			void tick().then(() => {
				if (drawer && open) sidebar?.querySelector('button')?.focus();
			});
		}
	});

	type NavItem = { label: string; href: string; icon: IconifyIcon; exact?: boolean };

	const items = $derived<NavItem[]>([
		{ label: 'Home', href: '/', icon: iconHome, exact: true },
		{ label: 'Suppliers', href: '/suppliers', icon: iconSuppliers },
		{ label: 'Deliver a request', href: '/orders', icon: iconDeliver, exact: true },
		{ label: 'My orders', href: '/orders/mine', icon: iconHistory },
		{ label: 'Profile', href: '/profile', icon: iconProfile },
		...(authService.user?.role === UserRole.SUPER_ADMIN || authService.user?.role === UserRole.ADMIN
			? [{ label: 'Administration', href: '/admin', icon: iconAdmin }]
			: [])
	]);

	function isActive(item: NavItem): boolean {
		const path = page.url.pathname;
		return item.exact ? path === item.href : path === item.href || path.startsWith(item.href + '/');
	}
</script>

<aside
	bind:this={sidebar}
	id="app-sidebar"
	class="sidebar"
	class:drawer
	class:open
	aria-label="App navigation"
	inert={drawer && !open}
>
	<div class="top">
		<a class="brand" href="/" aria-label="Friend on Campus home">FoC</a>
		{#if drawer}
			<Button variant="text" iconType="full" aria-label="Close menu" onclick={onclose}>
				<Icon icon={iconClose} />
			</Button>
		{/if}
	</div>

	<div class="account">
		<div class="identity">
			<ProfileAvatar
				url={profileService.profile?.profilePictureUrl}
				name={authService.user?.displayName ?? ''}
				size={32}
				expiresAt={profileService.profile?.profilePictureExpiresAt}
				onerror={() => void profileService.refreshPicture()}
			/>
			<span class="name">{authService.user?.displayName}</span>
		</div>
		<CreditBalance />
	</div>

	<Button variant="filled" iconType="left" href="/orders/new">
		<Icon icon={iconAdd} />Make a request
	</Button>

	<nav aria-label="Main">
		<ul>
			{#each items as item (item.href)}
				<li>
					<a class="nav-link" href={item.href} aria-current={isActive(item) ? 'page' : undefined}>
						<Icon icon={item.icon} size={24} />{item.label}
					</a>
				</li>
			{/each}
		</ul>
	</nav>

	<OngoingRequests />

	<div class="bottom">
		<Button variant="text" iconType="left" disabled={signingOut} onclick={onsignout}>
			<Icon icon={iconLogout} />{signingOut ? 'Signing out…' : 'Sign out'}
		</Button>
	</div>
</aside>

{#if drawer && open}
	<!-- The scrim is a pointer convenience; Escape and the close button also dismiss the drawer. -->
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div class="scrim" onclick={onclose}></div>
{/if}

<style>
	.sidebar {
		position: sticky;
		top: 0;
		height: 100dvh;
		width: 17rem;
		flex: 0 0 17rem;
		display: grid;
		grid-template-rows: auto auto auto auto minmax(0, 1fr) auto;
		gap: 1rem;
		padding: 1rem 0.75rem;
		background: var(--m3c-surface-container-low);
		border-inline-end: 1px solid var(--m3c-outline-variant);
		overflow: hidden;
	}

	.sidebar.drawer {
		position: fixed;
		inset: 0 auto 0 0;
		z-index: 50;
		width: min(20rem, 85vw);
		border-radius: 0 var(--m3-shape-large) var(--m3-shape-large) 0;
		transform: translateX(-105%);
		transition: transform var(--m3-easing-spatial, 0.3s ease);
		box-shadow: var(--m3-elevation-1, 0 1px 3px rgb(0 0 0 / 0.2));
	}

	.sidebar.drawer.open {
		transform: none;
	}

	.top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0 0.75rem;
		min-height: 2.5rem;
	}

	.brand {
		font-size: 1.5rem;
		font-weight: 800;
		letter-spacing: -0.03em;
		text-decoration: none;
		color: var(--m3c-primary);
	}

	.account {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0 0.75rem;
	}

	.identity { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }

	.name {
		font-weight: 650;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.account :global(.credit-balance) {
		padding: 0;
	}

	nav ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0.15rem;
	}

	.nav-link {
		display: flex;
		align-items: center;
		gap: 1rem;
		min-height: 3rem;
		padding: 0 1rem;
		border-radius: var(--m3-shape-full, 999px);
		color: var(--m3c-on-surface-variant);
		text-decoration: none;
	}

	.nav-link:hover {
		background: color-mix(in srgb, var(--m3c-on-surface) 8%, transparent);
	}

	.nav-link:focus-visible {
		outline: 2px solid var(--m3c-primary);
		outline-offset: -2px;
	}

	.nav-link[aria-current='page'] {
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
		font-weight: 650;
	}

	.bottom {
		display: flex;
	}

	.scrim {
		position: fixed;
		inset: 0;
		z-index: 40;
		background: rgb(0 0 0 / 0.32);
	}

	@media (prefers-reduced-motion: reduce) {
		.sidebar.drawer {
			transition: none;
		}
	}
</style>
