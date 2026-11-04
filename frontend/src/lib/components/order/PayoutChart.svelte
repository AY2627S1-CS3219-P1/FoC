<script lang="ts">
	import { payableReward } from '$lib/services/order/presentation';

	let {
		reward,
		allowedMs,
		elapsedMs,
		delivered = false
	}: {
		reward: bigint;
		allowedMs: number;
		/** Time since pickup: now while delivering, or at delivery. */
		elapsedMs: number;
		delivered?: boolean;
	} = $props();

	// Plot area in SVG units. The x axis runs from pickup to twice the allowed time.
	const width = 280;
	const height = 120;
	const pad = { top: 12, right: 12, bottom: 22, left: 12 };
	const plotW = width - pad.left - pad.right;
	const plotH = height - pad.top - pad.bottom;

	const payable = $derived(payableReward(reward, elapsedMs, allowedMs));
	const lost = $derived(reward - payable);
	const x = (ms: number) => pad.left + (Math.min(Math.max(ms, 0), 2 * allowedMs) / (2 * allowedMs)) * plotW;
	const y = (credits: bigint) => pad.top + plotH - (reward > 0n ? Number((credits * 1000n) / reward) / 1000 : 0) * plotH;

	const curve = $derived(`M ${x(0)} ${y(reward)} L ${x(allowedMs)} ${y(reward)} L ${x(2 * allowedMs)} ${y(0n)}`);
	const point = $derived({ cx: x(elapsedMs), cy: y(payable) });
	const minutes = (ms: number) => Math.round(ms / 60_000);
</script>

<figure class="payout">
	<div class="figure-head">
		<span class="amount">{payable.toString()}<span class="unit">credits</span></span>
		<span class="lost">{minutes(elapsedMs - allowedMs)} min late{lost > 0n ? ', ' + lost.toString() + ' credits lost' : ''}</span>
	</div>
	<svg viewBox="0 0 {width} {height}" role="img" aria-label={`Payout falls from ${reward} credits at ${minutes(allowedMs)} minutes after pickup to 0 at ${minutes(2 * allowedMs)} minutes. ${delivered ? 'Delivered' : 'Now'} at ${minutes(elapsedMs)} minutes: ${payable} credits.`}>
		<line class="axis" x1={pad.left} y1={y(0n)} x2={width - pad.right} y2={y(0n)} />
		<line class="deadline" x1={x(allowedMs)} y1={pad.top} x2={x(allowedMs)} y2={y(0n)} />
		<path class="curve" d={curve} />
		<circle class="now" cx={point.cx} cy={point.cy} r="5" />
		<text class="tick" x={pad.left} y={height - 6}>Pickup</text>
		<text class="tick" x={x(allowedMs)} y={height - 6} text-anchor="middle">{minutes(allowedMs)} min</text>
		<text class="tick" x={width - pad.right} y={height - 6} text-anchor="end">{minutes(2 * allowedMs)} min</text>
	</svg>
</figure>

<style>
	.payout {
		display: grid;
		gap: 0.5rem;
		margin: 0;
	}

	.figure-head {
		display: grid;
		gap: 0.1rem;
	}

	.amount {
		font-size: 2.25rem;
		font-weight: 750;
		line-height: 1;
		font-variant-numeric: tabular-nums;
	}

	.unit {
		margin-inline-start: 0.35rem;
		font-size: 1rem;
		font-weight: 600;
	}

	.lost {
		font-size: 0.875rem;
		font-weight: 600;
		color: var(--m3c-error);
	}

	svg {
		width: 100%;
		height: auto;
		overflow: visible;
	}

	.axis {
		stroke: var(--m3c-outline-variant);
	}

	.deadline {
		stroke: var(--m3c-outline);
		stroke-dasharray: 3 3;
	}

	.curve {
		fill: none;
		stroke: var(--m3c-primary);
		stroke-width: 2.5;
		stroke-linejoin: round;
	}

	.now {
		fill: var(--m3c-error);
		stroke: var(--m3c-surface);
		stroke-width: 2;
	}

	.tick {
		font-size: 10px;
		fill: var(--m3c-on-surface-variant);
	}

</style>
