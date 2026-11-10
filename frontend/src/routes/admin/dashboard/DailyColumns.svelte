<script lang="ts">
	import { dateKey, formatLongDate, formatShortDate } from '$lib/calendar-date';
	import { barPercent } from '$lib/services/order-admin/stats';
	import type { ChartPoint, DailySummary } from './daily';
	import { formatCount } from './format';

	let {
		points,
		summary,
		label,
		unit,
		detailHeading
	}: {
		points: ChartPoint[];
		summary: DailySummary;
		/** Names the chart for assistive technology, for example "Orders created per day". */
		label: string;
		/** The value's unit in the tooltip and table header, for example "orders". */
		unit: string;
		detailHeading?: string;
	} = $props();

	let active = $state<number | undefined>();
	const activePoint = $derived(active === undefined ? undefined : points[active]);

	function center(index: number): number {
		return ((index + 0.5) / points.length) * 100;
	}

	// Keep the tooltip inside the plot near either edge.
	function align(index: number): 'start' | 'middle' | 'end' {
		const position = center(index);
		if (position < 15) return 'start';
		if (position > 85) return 'end';
		return 'middle';
	}

	// Edge labels sit flush with the plot's edges instead of centring on their column.
	function tickLeft(index: number): number {
		const alignment = align(index);
		if (alignment === 'start') return 0;
		if (alignment === 'end') return 100;
		return center(index);
	}
</script>

<figure class="daily-chart">
	<div class="chart-body" role="img" aria-label={`${label}. The table below lists every day.`}>
		<div class="y-axis" aria-hidden="true">
			<span>{formatCount(summary.scaleMax)}</span>
			<span>0</span>
		</div>
		<div class="plot-area">
			<div class="plot" aria-hidden="true" onpointerleave={() => (active = undefined)}>
				{#each points as point, index (dateKey(point.date))}
					<div
						class="slot"
						class:active={active === index}
						onpointerenter={() => (active = index)}
						aria-hidden="true"
					>
						{#if point.value > 0n}
							<span
								class="column"
								style:height={`${Math.max(barPercent(point.value, summary.scaleMax), 1.5)}%`}
							></span>
						{/if}
					</div>
				{/each}
				{#if active !== undefined && activePoint}
					<div class={`tooltip ${align(active)}`} style:left={`${center(active)}%`} aria-hidden="true">
						<strong>{formatCount(activePoint.value)} {unit}</strong>
						{#if activePoint.detail}<span>{activePoint.detail}</span>{/if}
						<span>{formatLongDate(activePoint.date)}</span>
					</div>
				{/if}
			</div>
			<div class="x-axis" aria-hidden="true">
				{#each summary.ticks as index (index)}
					<span class={align(index)} style:left={`${tickLeft(index)}%`}>
						{formatShortDate(points[index].date)}
					</span>
				{/each}
			</div>
		</div>
	</div>

	<details class="table-view">
		<summary>Show as table</summary>
		<table>
			<caption class="visually-hidden">{label}</caption>
			<thead>
				<tr>
					<th scope="col">Date</th>
					<th scope="col" class="number">{unit[0].toUpperCase() + unit.slice(1)}</th>
					{#if detailHeading}<th scope="col" class="number">{detailHeading}</th>{/if}
				</tr>
			</thead>
			<tbody>
				{#each points as point (dateKey(point.date))}
					<tr>
						<th scope="row">{formatLongDate(point.date)}</th>
						<td class="number">{formatCount(point.value)}</td>
						{#if detailHeading}<td class="number">{point.detail ?? ''}</td>{/if}
					</tr>
				{/each}
			</tbody>
		</table>
	</details>
</figure>

<style>
	/* Top padding clears the axis maximum's label, which is centred on the top gridline. */
	.daily-chart {
		display: grid;
		gap: 0.75rem;
		margin: 0;
		padding-top: 1rem;
	}

	.chart-body {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: 0.5rem;
	}

	.y-axis {
		display: flex;
		flex-direction: column;
		justify-content: space-between;
		height: 10rem;
		font-size: 0.75rem;
		text-align: right;
		font-variant-numeric: tabular-nums;
		color: var(--m3c-on-surface-variant);
	}

	.y-axis span {
		line-height: 1;
		transform: translateY(-50%);
	}

	.y-axis span:last-child {
		transform: translateY(50%);
	}

	.plot-area {
		display: grid;
		gap: 0.35rem;
		min-width: 0;
	}

	/* A recessive top gridline at the axis maximum, and a solid baseline. */
	.plot {
		position: relative;
		display: flex;
		align-items: stretch;
		gap: 2px;
		height: 10rem;
		border-top: 1px dashed var(--m3c-outline-variant);
		border-bottom: 1px solid var(--m3c-outline);
	}

	/* The whole slot is the hover target, so short and empty days are easy to hit. */
	.slot {
		display: flex;
		flex: 1 1 0;
		align-items: flex-end;
		justify-content: center;
		min-width: 0;
		border-radius: 4px 4px 0 0;
	}

	.slot.active {
		background: var(--m3c-surface-container-high);
	}

	.column {
		display: block;
		width: 100%;
		max-width: 24px;
		border-radius: 4px 4px 0 0;
		background: var(--m3c-primary);
	}

	.slot.active .column {
		background: color-mix(in srgb, var(--m3c-primary) 80%, var(--m3c-surface));
	}

	.tooltip {
		position: absolute;
		bottom: calc(100% + 0.5rem);
		z-index: 1;
		display: grid;
		gap: 0.1rem;
		padding: 0.5rem 0.65rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-small, 8px);
		background: var(--m3c-surface-container-highest);
		box-shadow: 0 2px 6px rgb(0 0 0 / 0.15);
		color: var(--m3c-on-surface-variant);
		font-size: 0.8rem;
		white-space: nowrap;
		pointer-events: none;
	}

	/* The value leads; the date and any detail follow in secondary ink. */
	.tooltip strong {
		color: var(--m3c-on-surface);
		font-size: 0.95rem;
	}

	.x-axis {
		position: relative;
		height: 1rem;
		font-size: 0.75rem;
		color: var(--m3c-on-surface-variant);
	}

	.x-axis span {
		position: absolute;
		white-space: nowrap;
	}

	.middle {
		transform: translateX(-50%);
	}

	.end {
		transform: translateX(-100%);
	}

	.table-view summary {
		cursor: pointer;
		width: fit-content;
		color: var(--m3c-primary);
		font-size: 0.875rem;
	}

	.table-view table {
		width: 100%;
		margin-top: 0.5rem;
		border-collapse: collapse;
		font-size: 0.875rem;
	}

	.table-view th,
	.table-view td {
		padding: 0.3rem 0.5rem 0.3rem 0;
		border-bottom: 1px solid var(--m3c-outline-variant);
		text-align: left;
		font-weight: normal;
	}

	.table-view thead th {
		color: var(--m3c-on-surface-variant);
	}

	.table-view .number {
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
</style>
