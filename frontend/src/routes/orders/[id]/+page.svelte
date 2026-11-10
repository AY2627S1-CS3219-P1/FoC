<script lang="ts">
	import { page } from '$app/state';
	import { env } from '$env/dynamic/public';
	import { Button, Card, Dialog, LoadingIndicator } from 'm3-svelte';
	import { authService, orderApi, orderOffersApi, participantActionsApi, deliveryProofApi } from '$lib/services';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';
	import { durationNanos, isTerminal, orderDeadline, statusLabel, timestampNanos } from '$lib/services/order/presentation';
	import { describeOrderError } from '$lib/services/order/errors';
	import OrderProgress from './OrderProgress.svelte';
	import PayoutChart from './PayoutChart.svelte';
	import ParticipantActions from '$lib/components/order-cancellation/ParticipantActions.svelte';
	import OrderOfferControls from '$lib/components/order-offers/OrderOfferControls.svelte';
	import DeliveryProofPanel from '$lib/components/order/DeliveryProofPanel.svelte';
	import ReviewSection from './ReviewSection.svelte';

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
		window.addEventListener('online', visible);
		return () => { clearInterval(timer); document.removeEventListener('visibilitychange', visible); window.removeEventListener('online', visible); };
	});

	const deadline = $derived(order ? orderDeadline(order) : undefined);
	const seconds = $derived(deadline ? Math.max(0, Math.ceil((deadline - now) / 1000)) : undefined);

	// Roles and timing that decide what the page shows.
	const callerId = $derived(authService.user?.id ?? '');
	const isRequester = $derived(Boolean(order && order.requesterId === callerId));
	const isCourier = $derived(Boolean(order && order.courierId === callerId));
	// Payout inputs in integer nanoseconds, as Credit computes them.
	const allowedNanos = $derived(order?.deliveryDuration ? durationNanos(order.deliveryDuration) : 0n);
	const elapsedNanos = $derived.by(() => {
		if (!order?.pickedUpAt) return 0n;
		const end = order.deliveredAt ? timestampNanos(order.deliveredAt) : BigInt(now) * 1_000_000n;
		return end - timestampNanos(order.pickedUpAt);
	});
	const deliveryMinutes = $derived(Number(allowedNanos / 60_000_000_000n));
	// Wireframe: the payout graph only appears once delivery runs late.
	const late = $derived(
		Boolean(order?.pickedUpAt) && allowedNanos > 0n && elapsedNanos > allowedNanos &&
			(order?.status === OrderStatus.DELIVERING || Boolean(order?.deliveredAt))
	);
	const canChat = $derived(
		Boolean(order && (isRequester || isCourier) && order.courierId && !isTerminal(order.status))
	);
	// Either participant may report the other once a courier is assigned.
	const reportSubjectId = $derived(
		order && order.courierId && (isRequester || isCourier) ? (isRequester ? order.courierId : order.requesterId) : ''
	);
	const reportHref = $derived(
		order && reportSubjectId
			? `/reports/new?orderId=${encodeURIComponent(order.id)}&subjectId=${encodeURIComponent(reportSubjectId)}`
			: ''
	);
	const telegramBotUrl = env.PUBLIC_TELEGRAM_BOT_URL ?? '';

	// Courier's "Update status" opens proof of delivery in a dialog.
	let proofOpen = $state(false);
	$effect(() => {
		if (order && order.status !== OrderStatus.DELIVERING) proofOpen = false;
	});

	const clock = new Intl.DateTimeFormat('en-SG', { hour: 'numeric', minute: '2-digit', timeZone: 'Asia/Singapore' });
	function countdown(total: number): string {
		const minutes = Math.floor(total / 60);
		return minutes >= 60 ? `${Math.floor(minutes / 60)} h ${minutes % 60} min` : `${minutes} min ${total % 60} s`;
	}
</script>

<svelte:head><title>{order?.itemDescription ?? 'Request'} · Friend on Campus</title></svelte:head>

{#snippet proofButtons()}
	<Button variant="text" onclick={() => (proofOpen = false)}>Close</Button>
{/snippet}

<section class="order-page" aria-labelledby="order-title">
	<div class="top">
		<Button variant="text" href={isRequester || isCourier ? '/orders/mine' : '/orders'}>{isRequester || isCourier ? 'My orders' : 'Open requests'}</Button>
		<Button variant="text" disabled={loading} onclick={() => void refresh()}>{loading ? 'Refreshing…' : 'Refresh'}</Button>
	</div>

	{#if !authService.user}<p class="notice"><a href="/login">Sign in</a> to view this request.</p>
	{:else}
		{#if error}<p class="notice error" role="alert">{order ? 'This request may be out of date. ' : ''}{error}</p>{/if}
		{#if !order && loading}<div class="loading-state"><LoadingIndicator aria-label="Loading request" /></div>{/if}
		{#if order}
			<div class="layout">
				<div class="main">
					<Card variant="outlined">
						<div class="panel-body">
							<header class="title">
								<p class="status">{statusLabel(order.status)}</p>
								<h1 id="order-title">{order.itemDescription}</h1>
								{#if isCourier || isRequester}<p class="sub">{isCourier ? "You're the courier for this request." : 'You requested this.'}</p>{/if}
							</header>
							{#if order.closeReason && isTerminal(order.status)}<p class="notice">{order.closeReason}</p>{/if}
							<dl class="facts">
								<div><dt>Pick up</dt><dd>{order.pickup?.name ?? 'Unavailable'}<span class="sub">{order.pickup?.buildingName ?? ''}{order.pickup?.floor ? ', ' + order.pickup.floor : ''}</span></dd></div>
								<div><dt>Deliver to</dt><dd>
									{#if order.delivery?.location?.locationId}{order.delivery.location.name}<span class="sub">{order.delivery.location.buildingName}{order.delivery.location.floor ? ', ' + order.delivery.location.floor : ''}</span>
									{:else}{order.deliveryBuildingName || 'Area unavailable'}<span class="sub">The exact place is shared with the courier.</span>{/if}
								</dd></div>
								{#if order.delivery?.instructions}<div class="wide"><dt>Instructions</dt><dd class="prose">{order.delivery.instructions}</dd></div>{/if}
							</dl>
							{#if order.itemImages.length}
								<div class="images" aria-label="Item photos">
									{#each order.itemImages as image (image.id)}
										<a href={image.downloadUrl} target="_blank" rel="noreferrer"><img src={image.downloadUrl} alt="Item" loading="lazy" /></a>
									{/each}
								</div>
							{/if}
						</div>
					</Card>

					<Card variant="outlined">
						<div class="panel-body">
							<h2>Progress</h2>
							<OrderProgress {order} />
						</div>
					</Card>
				</div>

				<aside class="side" aria-label="Reward and actions">
					<Card variant="outlined">
						<div class="panel-body">
							{#if late}
								<PayoutChart reward={order.rewardCredits} {allowedNanos} {elapsedNanos} settled={order.status === OrderStatus.COMPLETED} />
							{:else}
								<div class="reward">
									<span class="amount">{order.rewardCredits.toString()}<span class="unit">credits</span></span>
									{#if order.status === OrderStatus.DELIVERING && seconds !== undefined}
										<span class="sub">Full reward if delivered by {clock.format(deadline!)}, in {countdown(seconds)}.</span>
									{:else if !order.pickedUpAt && deliveryMinutes}
										<span class="sub">Deliver within {deliveryMinutes} min of pickup for the full reward.</span>
									{/if}
									{#if order.status === OrderStatus.AWAITING_COURIER && seconds !== undefined}
										<span class="sub">{seconds > 0 ? `Open for couriers for ${countdown(seconds)}.` : 'Closing. Checking status…'}</span>
									{/if}
								</div>
							{/if}

							<div class="actions">
								{#if canChat && telegramBotUrl}
									<Button variant="tonal" href={telegramBotUrl} target="_blank" rel="noreferrer" title="Send /chat in the bot and choose this request">Chat on Telegram</Button>
								{/if}
								{#if isCourier && order.status === OrderStatus.DELIVERING}
									<Button onclick={() => (proofOpen = true)}>Update status: delivered</Button>
								{/if}
								{#if reportHref}
									<Button variant="text" href={reportHref}>Report the {isRequester ? 'courier' : 'requester'}</Button>
								{/if}
							</div>

							<ParticipantActions {order} {refresh} api={participantActionsApi} {callerId} />
						</div>
					</Card>

					{#if order.status === OrderStatus.AWAITING_COURIER}
						<Card variant="outlined">
							<div class="panel-body">
								<OrderOfferControls {order} {refresh} api={orderOffersApi} {callerId} suspended={authService.user?.role === UserRole.SUSPENDED_USER} />
							</div>
						</Card>
					{/if}

					{#if order.deliveredAt || (order.status !== OrderStatus.DELIVERING && order.pickedUpAt)}
						<Card variant="outlined">
							<div class="panel-body">
								{#key `${callerId}:${order.id}`}
									<DeliveryProofPanel {order} {refresh} api={deliveryProofApi} {callerId} />
								{/key}
							</div>
						</Card>
					{/if}
				</aside>
			</div>

			<ReviewSection {order} {callerId} />

			<Dialog bind:open={proofOpen} headline="Leave a photo as proof of delivery" buttons={proofButtons}>
				{#key `${callerId}:${order.id}:${proofOpen}`}
					<DeliveryProofPanel {order} {refresh} api={deliveryProofApi} {callerId} />
				{/key}
			</Dialog>
		{/if}
	{/if}
</section>

<style>
	.order-page { display: grid; gap: 1rem; max-width: 76rem; margin: 0 auto; }
	.top { display: flex; justify-content: space-between; gap: 0.5rem; }
	.layout { display: grid; grid-template-columns: minmax(0, 3fr) minmax(18rem, 2fr); gap: 1.5rem; align-items: start; }
	.main, .side { display: grid; gap: 1rem; min-width: 0; }
	.side { position: sticky; top: 1rem; }

	.title { display: grid; gap: 0.15rem; }
	.status { margin: 0; font-weight: 650; color: var(--m3c-primary); }
	h1 { margin: 0; font-size: clamp(1.5rem, 3vw, 2rem); line-height: 1.2; overflow-wrap: anywhere; }
	h2 { margin: 0; font-size: 1.1rem; }

	.facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; margin: 0; }
	.facts .wide { grid-column: 1 / -1; }
	dt { font-size: 0.85rem; font-weight: 600; color: var(--m3c-on-surface-variant); }
	dd { display: grid; margin: 0.15rem 0 0; font-weight: 600; overflow-wrap: anywhere; }
	.prose { font-weight: 400; white-space: pre-wrap; }
	.sub { font-size: 0.875rem; font-weight: 400; line-height: 1.4; color: var(--m3c-on-surface-variant); }

	.images { display: flex; flex-wrap: wrap; gap: 0.5rem; }
	img { width: 6rem; height: 6rem; object-fit: cover; border-radius: var(--m3-shape-small, 8px); }

	.reward { display: grid; gap: 0.35rem; }
	.amount { font-size: 2.25rem; font-weight: 750; line-height: 1; font-variant-numeric: tabular-nums; }
	.unit { margin-inline-start: 0.35rem; font-size: 1rem; font-weight: 600; }

	.actions { display: grid; justify-items: start; gap: 0.5rem; }

	@media (max-width: 60rem) {
		.layout { grid-template-columns: minmax(0, 1fr); }
		.side { position: static; grid-row: 1; }
	}
	@media (max-width: 36rem) {
		.facts { grid-template-columns: minmax(0, 1fr); }
	}
</style>
