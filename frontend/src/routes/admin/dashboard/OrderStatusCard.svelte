<script lang="ts">
	import { Card } from 'm3-svelte';
	import { orderAdminApi } from '$lib/services';
	import { barPercent, summarizeOrderStats, type OrderStatusRow } from '$lib/services/order-admin/stats';
	import CardState from './CardState.svelte';
	import { Loadable } from './loadable.svelte';
	import { formatCount } from './format';

	let { refresh }: { refresh: number } = $props();

	const stats = new Loadable(
		async () => summarizeOrderStats(await orderAdminApi.getOrderStats()),
		'order counts'
	);

	$effect(() => {
		void refresh;
		void stats.load();
	});

	function share(count: bigint, total: bigint): string {
		if (total === 0n) return '0% of all orders';
		return `${barPercent(count, total).toFixed(1)}% of all orders`;
	}
</script>

{#snippet bars(heading: string, rows: OrderStatusRow[], max: bigint, total: bigint)}
	<tbody>
		<tr class="group"><th scope="rowgroup" colspan="3">{heading}</th></tr>
		{#each rows as row (row.status)}
			<tr title={`${row.label}: ${formatCount(row.count)} (${share(row.count, total)})`}>
				<th scope="row">{row.label}</th>
				<td class="bar-cell" aria-hidden="true">
					{#if row.count > 0n}
						<span class="bar" style:width={`${Math.max(barPercent(row.count, max), 1)}%`}></span>
					{/if}
				</td>
				<td class="value">{formatCount(row.count)}</td>
			</tr>
		{/each}
	</tbody>
{/snippet}

<Card variant="outlined">
	<div class="panel-body">
		<div class="card-intro">
			<h2 id="order-stats-title">Orders by status</h2>
			<p class="secondary-text">Every order request, counted by its current status.</p>
		</div>
		<CardState source={stats} label="order counts">
			{#snippet children(summary)}
				<dl class="stat-tiles">
					<div><dt>Total</dt><dd>{formatCount(summary.total)}</dd></div>
					<div><dt>In progress</dt><dd>{formatCount(summary.active)}</dd></div>
					<div><dt>Finished</dt><dd>{formatCount(summary.finished)}</dd></div>
				</dl>
				<table class="status-bars" aria-labelledby="order-stats-title">
					<thead class="visually-hidden">
						<tr><th scope="col">Status</th><th scope="col">Share</th><th scope="col">Orders</th></tr>
					</thead>
					{@render bars(
						'In progress',
						summary.rows.filter((row) => !row.terminal),
						summary.max,
						summary.total
					)}
					{@render bars(
						'Finished',
						summary.rows.filter((row) => row.terminal),
						summary.max,
						summary.total
					)}
				</table>
			{/snippet}
		</CardState>
	</div>
</Card>

<style>
	.status-bars {
		width: 100%;
		border-collapse: collapse;
	}

	.status-bars th,
	.status-bars td {
		padding: 0.3rem 0;
		text-align: left;
		font-weight: normal;
	}

	.status-bars th[scope='row'] {
		width: 10.5rem;
		padding-right: 0.75rem;
		color: var(--m3c-on-surface-variant);
	}

	.group th {
		padding-top: 1rem;
		font-size: 0.8rem;
		font-weight: 650;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--m3c-on-surface-variant);
	}

	tbody tr:not(.group):hover {
		background: var(--m3c-surface-container-low);
	}

	.bar-cell {
		width: auto;
		border-left: 1px solid var(--m3c-outline-variant);
	}

	/* Thin single-series bars anchored at the baseline, rounded only at the data end. */
	.bar {
		display: block;
		height: 0.75rem;
		border-radius: 0 4px 4px 0;
		background: var(--m3c-primary);
	}

	.status-bars td.value {
		width: 4.5rem;
		padding-left: 0.75rem;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}

	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	@media (max-width: 36rem) {
		.status-bars th[scope='row'] {
			width: 8rem;
		}
	}
</style>
