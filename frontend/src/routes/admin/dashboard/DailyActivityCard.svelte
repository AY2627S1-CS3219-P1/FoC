<script lang="ts">
	import { untrack } from 'svelte';
	import { Button, Card, ConnectedButtons } from 'm3-svelte';
	import {
		dateKey,
		formatLongDate,
		lastDays,
		singaporeToday,
		type DateRange
	} from '$lib/calendar-date';
	import { creditAdminApi, orderAdminApi } from '$lib/services';
	import CardState from './CardState.svelte';
	import DailyColumns from './DailyColumns.svelte';
	import { formatAverage, summarizeDaily, type ChartPoint } from './daily';
	import { formatCount } from './format';
	import { Loadable } from './loadable.svelte';

	let { refresh }: { refresh: number } = $props();

	const presets = [7, 30, 90];
	let days = $state(30);
	let today = $state(singaporeToday());
	const range: DateRange = $derived(lastDays(days, today));

	function series(points: ChartPoint[]) {
		return { points, summary: summarizeDaily(points) };
	}

	const orders = new Loadable(async () => {
		const counts = await orderAdminApi.getDailyOrderCounts(range);
		return series(counts.map(({ date, count }) => ({ date, value: count })));
	}, 'daily orders');

	const volume = new Loadable(async () => {
		const volumes = await creditAdminApi.getDailyTransactionVolume(range);
		let payments = 0n;
		for (const day of volumes) payments += day.paymentCount;
		return {
			...series(
				volumes.map(({ date, creditsTransferred, paymentCount }) => ({
					date,
					value: creditsTransferred,
					detail: `${formatCount(paymentCount)} ${paymentCount === 1n ? 'payment' : 'payments'}`
				}))
			),
			payments
		};
	}, 'daily transaction volume');

	// A refresh moves the range to end on today's Singapore date if the day has changed.
	$effect(() => {
		void refresh;
		const now = singaporeToday();
		untrack(() => {
			if (dateKey(now) !== dateKey(today)) today = now;
		});
	});

	// A refresh or a new range re-reads both series.
	$effect(() => {
		void refresh;
		void range;
		untrack(() => {
			void orders.load();
			void volume.load();
		});
	});
</script>

{#snippet busiest(point: ChartPoint | undefined, unit: string)}
	{#if point}
		{formatCount(point.value)} {unit}<span class="tile-note">{formatLongDate(point.date)}</span>
	{:else}
		None
	{/if}
{/snippet}

<Card variant="outlined">
	<div class="panel-body">
		<div class="card-header">
			<div class="card-intro">
				<h2>Daily activity</h2>
				<p class="secondary-text">
					{formatLongDate(range.start)} to {formatLongDate(range.end)}, by Singapore day.
				</p>
			</div>
			<div role="radiogroup" aria-label="Date range">
				<ConnectedButtons>
					{#each presets as preset (preset)}
						<Button variant="tonal" label>
							<input type="radio" name="daily-range" value={preset} bind:group={days} />
							{preset} days
						</Button>
					{/each}
				</ConnectedButtons>
			</div>
		</div>

		<section class="series" aria-labelledby="daily-orders-title">
			<h3 id="daily-orders-title">Orders created</h3>
			<CardState source={orders} label="daily orders">
				{#snippet children({ points, summary })}
					<dl class="stat-tiles">
						<div><dt>Total</dt><dd>{formatCount(summary.total)}</dd></div>
						<div><dt>Average per day</dt><dd>{formatAverage(summary.average)}</dd></div>
						<div><dt>Busiest day</dt><dd>{@render busiest(summary.busiest, 'orders')}</dd></div>
					</dl>
					<DailyColumns {points} {summary} label="Orders created per day" unit="orders" />
				{/snippet}
			</CardState>
		</section>

		<section class="series" aria-labelledby="daily-volume-title">
			<div>
				<h3 id="daily-volume-title">Credits paid to couriers</h3>
				<p class="secondary-text">
					Settlements that paid a courier nothing are not counted as payments.
				</p>
			</div>
			<CardState source={volume} label="daily transaction volume">
				{#snippet children({ points, summary, payments })}
					<dl class="stat-tiles">
						<div><dt>Credits paid</dt><dd>{formatCount(summary.total)}</dd></div>
						<div><dt>Payments</dt><dd>{formatCount(payments)}</dd></div>
						<div><dt>Average credits per day</dt><dd>{formatAverage(summary.average)}</dd></div>
						<div><dt>Busiest day</dt><dd>{@render busiest(summary.busiest, 'credits')}</dd></div>
					</dl>
					<DailyColumns
						{points}
						{summary}
						label="Credits paid to couriers per day"
						unit="credits"
						detailHeading="Payments"
					/>
				{/snippet}
			</CardState>
		</section>
	</div>
</Card>

<style>
	.card-header {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1rem;
	}

	.series {
		display: grid;
		gap: 0.75rem;
	}

	.series + .series {
		padding-top: 1.25rem;
		border-top: 1px solid var(--m3c-outline-variant);
	}

	h3 {
		margin: 0;
		font-size: 1rem;
	}

	.series p {
		margin: 0.25rem 0 0;
	}

	.tile-note {
		display: block;
		margin-top: 0.15rem;
		font-size: 0.8rem;
		font-weight: normal;
		color: var(--m3c-on-surface-variant);
	}
</style>
