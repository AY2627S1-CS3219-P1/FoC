<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Button, Icon, LoadingIndicator, SelectOutlined } from 'm3-svelte';
	import iconBag from '@ktibow/iconset-material-symbols/shopping-bag-outline';
	import iconBike from '@ktibow/iconset-material-symbols/pedal-bike';
	import { OrderStatus, ParticipantRole, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import { authService, orderApi } from '$lib/services';
	import type { OrderPage } from '$lib/services/order';
	import { describeOrderError } from '$lib/services/order/errors';
	import { orderStatuses, roleFromQuery, statusLabel } from '$lib/services/order/presentation';

	// URL state: view (active | all), role, status (all view only) and page.
	const params = $derived(page.url.searchParams);
	const view = $derived(params.get('view') === 'all' ? 'all' : 'active');
	const role = $derived(roleFromQuery(params.get('role')));
	const status = $derived(view === 'all' ? (Number(params.get('status')) as OrderStatus) || OrderStatus.UNSPECIFIED : OrderStatus.UNSPECIFIED);
	const pageNumber = $derived(Math.max(1, Number(params.get('page')) || 1));

	let result = $state<OrderPage | null>(null);
	let error = $state('');
	let loading = $state(false);
	let revision = $state(0);

	$effect(() => {
		const caller = authService.user?.id;
		const query = { activeOnly: view === 'active', role, status, page: pageNumber };
		void revision;
		error = '';
		if (!caller) return;
		let active = true;
		loading = true;
		orderApi
			.listMyOrders(query)
			.then((value) => active && (result = value))
			.catch((cause) => active && (error = describeOrderError(cause)))
			.finally(() => active && (loading = false));
		return () => (active = false);
	});

	function hrefWith(changes: Record<string, string | null>): string {
		const next = new URLSearchParams(params);
		for (const [name, value] of Object.entries(changes)) {
			if (value) next.set(name, value);
			else next.delete(name);
		}
		if (!('page' in changes)) next.delete('page');
		return page.url.pathname + (next.size ? '?' + next : '');
	}

	const roleOptions: { value: string; label: string }[] = [
		{ value: '', label: 'All' },
		{ value: 'requester', label: 'Requested' },
		{ value: 'courier', label: 'Delivered' }
	];
	const roleParam = $derived(role === ParticipantRole.REQUESTER ? 'requester' : role === ParticipantRole.COURIER ? 'courier' : '');

	const date = new Intl.DateTimeFormat('en-SG', { day: 'numeric', month: 'short', timeZone: 'Asia/Singapore' });
	const requested = (order: Order) => order.requesterId === authService.user?.id;
</script>

<svelte:head><title>My orders · Friend on Campus</title></svelte:head>

<section class="mine" aria-labelledby="mine-title">
	<header class="heading">
		<h1 id="mine-title">My orders</h1>
		<Button variant="text" size="s" disabled={loading} onclick={() => revision++}>Refresh</Button>
	</header>

	<nav class="tabs" aria-label="Which orders">
		<a href={hrefWith({ view: null, status: null })} aria-current={view === 'active' ? 'page' : undefined}>Active</a>
		<a href={hrefWith({ view: 'all' })} aria-current={view === 'all' ? 'page' : undefined}>All</a>
	</nav>

	<div class="filters">
		<div class="roles" role="group" aria-label="Your role">
			{#each roleOptions as option (option.value)}
				<a class="chip" href={hrefWith({ role: option.value || null })} aria-current={roleParam === option.value ? 'true' : undefined}>{option.label}</a>
			{/each}
		</div>
		{#if view === 'all'}
			<div class="status">
				<SelectOutlined
					label="Status"
					width="100%"
					value={status ? String(status) : ''}
					options={[{ value: '', text: 'Any status' }, ...orderStatuses.map(([value, text]) => ({ value: String(value), text }))]}
					onchange={(event) => goto(hrefWith({ status: event.currentTarget.value || null }), { keepFocus: true, noScroll: true })}
				/>
			</div>
		{/if}
	</div>

	<div class="results" aria-live="polite">
		{#if error}
			<p class="notice error" role="alert">{error}</p>
			<div><Button variant="outlined" onclick={() => revision++}>Try again</Button></div>
		{:else if loading && !result}
			<div class="loading-state"><LoadingIndicator aria-label="Loading your orders" /></div>
		{:else if result}
			{#if !result.orders.length}
				<p class="muted">
					{#if view === 'active'}Nothing in progress. <a href="/orders/new">Make a request</a> or <a href="/orders">deliver one</a>.
					{:else}No orders match these filters.{/if}
				</p>
			{:else}
				<ul class="rows">
					{#each result.orders as order (order.id)}
						{@const mine = requested(order)}
						<li>
							<a class="row" href={'/orders/' + encodeURIComponent(order.id)}>
								<span class="role" class:courier={!mine} title={mine ? 'You requested this' : 'You delivered or are delivering this'}>
									<Icon icon={mine ? iconBag : iconBike} size={18} />
									<span class="visually-hidden">{mine ? 'You requested:' : 'You delivered:'}</span>
								</span>
								<span class="text">
									<span class="title-line">
										<span class="title">{order.itemDescription}</span>
										<span class="reward">{order.rewardCredits.toString()} credits</span>
									</span>
									<span class="meta">{statusLabel(order.status)}, from {order.pickup?.name ?? 'pickup'} to {order.deliveryBuildingName || 'an unlisted area'}</span>
								</span>
								{#if order.createdAt}<time class="date">{date.format(timestampDate(order.createdAt))}</time>{/if}
							</a>
						</li>
					{/each}
				</ul>
			{/if}
			{#if result.totalPages > 1}
				<nav class="pagination" aria-label="Order pages">
					<Button variant="outlined" disabled={result.page <= 1} onclick={() => goto(hrefWith({ page: String(result!.page - 1) }))}>Previous</Button>
					<span>Page {result.page} of {result.totalPages}</span>
					<Button variant="outlined" disabled={result.page >= result.totalPages} onclick={() => goto(hrefWith({ page: String(result!.page + 1) }))}>Next</Button>
				</nav>
			{/if}
		{/if}
	</div>
</section>

<style>
	.mine { display: grid; gap: 1rem; max-width: 52rem; margin: 0 auto; }
	.heading { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
	h1 { margin: 0; font-size: clamp(1.6rem, 3vw, 2rem); }
	.muted { margin: 0; color: var(--m3c-on-surface-variant); }

	/* Tabs and chips are app-owned links: each choice is a URL. */
	.tabs { display: flex; gap: 0.25rem; border-bottom: 1px solid var(--m3c-outline-variant); }
	.tabs a {
		padding: 0.6rem 1rem;
		margin-bottom: -1px;
		border-bottom: 3px solid transparent;
		color: var(--m3c-on-surface-variant);
		font-weight: 600;
		text-decoration: none;
	}
	.tabs a[aria-current] { border-bottom-color: var(--m3c-primary); color: var(--m3c-primary); }

	.filters { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 0.75rem; }
	.roles { display: flex; flex-wrap: wrap; gap: 0.5rem; }
	.chip {
		padding: 0.35rem 0.85rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-small, 8px);
		color: var(--m3c-on-surface-variant);
		font-size: 0.875rem;
		font-weight: 600;
		text-decoration: none;
	}
	.chip[aria-current] { border-color: transparent; background: var(--m3c-secondary-container); color: var(--m3c-on-secondary-container); }
	.tabs a:focus-visible, .chip:focus-visible, .row:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: -2px; }
	.status { width: min(100%, 16rem); }

	.results { display: grid; gap: 0.75rem; }
	.rows { display: grid; gap: 0.25rem; margin: 0; padding: 0; list-style: none; }
	.row {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.75rem 1rem;
		border-radius: var(--m3-shape-medium);
		color: inherit;
		text-decoration: none;
	}
	.row:hover { background: var(--m3c-surface-container); }

	.role {
		flex: 0 0 auto;
		display: grid;
		place-items: center;
		width: 2rem;
		height: 2rem;
		border-radius: 50%;
		background: var(--m3c-primary-container);
		color: var(--m3c-on-primary-container);
	}
	.role.courier { background: var(--m3c-tertiary-container); color: var(--m3c-on-tertiary-container); }

	.text { flex: 1; display: grid; gap: 0.1rem; min-width: 0; }
	.title-line { display: flex; align-items: baseline; justify-content: space-between; gap: 0.5rem; min-width: 0; }
	.title { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
	.reward { flex: 0 0 auto; font-weight: 650; }
	.meta { font-size: 0.875rem; color: var(--m3c-on-surface-variant); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.date { flex: 0 0 auto; font-size: 0.85rem; color: var(--m3c-on-surface-variant); }

	.pagination { display: flex; align-items: center; justify-content: center; gap: 1rem; }
	.visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }

	@media (max-width: 36rem) {
		.date { display: none; }
	}
</style>
