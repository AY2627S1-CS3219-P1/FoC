<script lang="ts">
	import type { Timestamp } from '@bufbuild/protobuf/wkt';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { OrderStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';

	let { order }: { order: Order } = $props();

	type Step = { label: string; at?: Timestamp; done: boolean };

	// The lifecycle is a real sequence, so it is shown as one.
	const steps = $derived<Step[]>([
		{ label: 'Request created', at: order.createdAt, done: true },
		{ label: 'Courier accepted', at: order.assignedAt, done: Boolean(order.assignedAt) },
		{ label: 'Picked up', at: order.pickedUpAt, done: Boolean(order.pickedUpAt) },
		{ label: 'Delivered', at: order.deliveredAt, done: Boolean(order.deliveredAt) },
		{ label: 'Receipt confirmed', at: order.completedAt, done: Boolean(order.completedAt) }
	]);
	const current = $derived(steps.findIndex((step) => !step.done));
	const stopped = $derived(
		[OrderStatus.CANCELLED, OrderStatus.EXPIRED, OrderStatus.FAILED, OrderStatus.REJECTED].includes(order.status)
	);

	// Times are Singapore time, so "today" is the Singapore calendar day too.
	const time = new Intl.DateTimeFormat('en-SG', { hour: 'numeric', minute: '2-digit', timeZone: 'Asia/Singapore' });
	const day = new Intl.DateTimeFormat('en-SG', { day: 'numeric', month: 'short', timeZone: 'Asia/Singapore' });
	const calendarDay = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Singapore' });
	function when(at?: Timestamp): string {
		if (!at) return '';
		const date = timestampDate(at);
		const today = calendarDay.format(new Date()) === calendarDay.format(date);
		return today ? time.format(date) : `${day.format(date)}, ${time.format(date)}`;
	}
</script>

<ol class="progress" aria-label="Request progress">
	{#each steps as step, index (step.label)}
		<li class:done={step.done} class:current={!stopped && index === current} aria-current={!stopped && index === current ? 'step' : undefined}>
			<span class="marker" aria-hidden="true"></span>
			<span class="label">{step.label}</span>
			{#if step.at && step.done}<time class="time">{when(step.at)}</time>{/if}
		</li>
	{/each}
</ol>

<style>
	.progress {
		display: grid;
		gap: 0;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	li {
		position: relative;
		display: grid;
		grid-template-columns: 1.5rem 1fr auto;
		align-items: center;
		gap: 0.75rem;
		min-height: 2.75rem;
		color: var(--m3c-on-surface-variant);
	}

	/* The connector runs between markers. */
	li:not(:last-child)::after {
		content: '';
		position: absolute;
		left: calc(0.75rem - 1px);
		top: calc(50% + 0.6rem);
		height: calc(100% - 1.2rem);
		width: 2px;
		background: var(--m3c-outline-variant);
	}

	li.done:not(:last-child)::after {
		background: var(--m3c-primary);
	}

	.marker {
		justify-self: center;
		width: 0.85rem;
		height: 0.85rem;
		border-radius: 50%;
		border: 2px solid var(--m3c-outline);
		background: var(--m3c-surface);
	}

	.done .marker {
		border-color: var(--m3c-primary);
		background: var(--m3c-primary);
	}

	.current .marker {
		width: 1.1rem;
		height: 1.1rem;
		border-color: var(--m3c-primary);
		box-shadow: 0 0 0 4px var(--m3c-primary-container);
	}

	.done,
	.current {
		color: var(--m3c-on-surface);
	}

	.current .label {
		font-weight: 650;
	}

	.time {
		font-size: 0.85rem;
		font-variant-numeric: tabular-nums;
		color: var(--m3c-on-surface-variant);
	}
</style>
