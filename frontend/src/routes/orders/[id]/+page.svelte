<script lang="ts">
	import { page } from '$app/state';
	import { Button, LoadingIndicator } from 'm3-svelte';
	import { authService, orderApi, orderOffersApi, participantActionsApi } from '$lib/services';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import type { Order } from '$lib/gen/order/v1/shared_types_pb';
	import { isTerminal, orderDeadline } from '$lib/services/order/presentation';
	import { describeOrderError } from '$lib/services/order/errors';
	import OrderDetailShell from '$lib/components/order/OrderDetailShell.svelte';
	import ParticipantActions from '$lib/components/order-cancellation/ParticipantActions.svelte';
	import OrderOfferControls from '$lib/components/order-offers/OrderOfferControls.svelte';

	let order = $state<Order | null>(null);
	let error = $state('');
	let loading = $state(false);
	let now = $state(Date.now());
	let generation = 0;

	async function refresh() {
		const id = page.params.id!;
		const identity = authService.user?.id;
		if (!identity) return;
		const request = ++generation;
		loading = true;
		try {
			const value = await orderApi.getOrder(id);
			if (request === generation && page.params.id === id && authService.user?.id === identity) {
				order = value;
				error = '';
			}
		} catch (cause) {
			if (request === generation) error = describeOrderError(cause, 'refresh this order');
		} finally {
			if (request === generation) loading = false;
		}
	}

	$effect(() => {
		const id = page.params.id;
		const user = authService.user?.id;
		generation++;
		order = null;
		error = '';
		if (id && user) void refresh();
		return () => { generation++; };
	});

	$effect(() => {
		if (!authService.user) return;
		let lastPoll = Date.now();
		let checkedDeadline: number | undefined;
		const timer = setInterval(() => {
			now = Date.now();
			if (document.hidden || !order || isTerminal(order.status)) return;
			const deadline = orderDeadline(order);
			if (deadline && now >= deadline && checkedDeadline !== deadline) {
				checkedDeadline = deadline;
				lastPoll = now;
				void refresh();
			} else if (!loading && now - lastPoll >= 15000) {
				lastPoll = now;
				void refresh();
			}
		}, 1000);
		const visible = () => {
			if (!document.hidden) { lastPoll = Date.now(); void refresh(); }
		};
		document.addEventListener('visibilitychange', visible);
		return () => { clearInterval(timer); document.removeEventListener('visibilitychange', visible); };
	});

	const deadline = $derived(order ? orderDeadline(order) : undefined);
	const seconds = $derived(deadline ? Math.max(0, Math.ceil((deadline - now) / 1000)) : undefined);
</script>

<svelte:head><title>Order details · Friend on Campus</title></svelte:head>
<section class="page-content" aria-label="Order detail">
	<nav><a href="/orders">Browse</a> · <a href="/orders/mine">My Orders</a></nav>
	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to read this order.</p>
	{:else}
		{#if error}<p class="notice error" role="alert">{order ? 'Showing previously loaded data. ' : ''}{error}</p>{/if}
		<Button variant="outlined" disabled={loading} onclick={() => void refresh()}>{loading ? 'Refreshing…' : 'Refresh order'}</Button>
		{#if !order && loading}<LoadingIndicator aria-label="Loading order" />{/if}
		{#if order}
			{#if seconds !== undefined}<p role="status">{seconds > 0 ? 'Time until deadline: ' + Math.floor(seconds / 60) + 'm ' + seconds % 60 + 's' : 'Deadline reached. Checking backend status.'}</p>{/if}
			<OrderDetailShell {order} {refresh}>
				{#snippet actions(currentOrder, refreshOrder)}
					<OrderOfferControls
						order={currentOrder}
						refresh={refreshOrder}
						api={orderOffersApi}
						callerId={authService.user?.id ?? ''}
						suspended={authService.user?.role === UserRole.SUSPENDED_USER}
					/>
					<ParticipantActions order={currentOrder} refresh={refreshOrder} api={participantActionsApi} callerId={authService.user?.id ?? ''} />
				{/snippet}
			</OrderDetailShell>
		{/if}
	{/if}
</section>
