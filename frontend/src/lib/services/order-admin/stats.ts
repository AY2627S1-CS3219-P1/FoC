import { isTerminal, statusLabel } from '../order/presentation.ts';
import type { OrderStatusCount } from './orderAdminApi.ts';

export interface OrderStatusRow extends OrderStatusCount {
	label: string;
	terminal: boolean;
}

export interface OrderStatsSummary {
	rows: OrderStatusRow[];
	total: bigint;
	/** Orders that can still change status. */
	active: bigint;
	/** Orders in a terminal status. */
	finished: bigint;
	/** The largest single count, used to scale bars; zero when there are no orders. */
	max: bigint;
}

export function summarizeOrderStats(counts: OrderStatusCount[]): OrderStatsSummary {
	const rows = counts.map((entry) => ({
		...entry,
		label: statusLabel(entry.status),
		terminal: isTerminal(entry.status)
	}));

	let active = 0n;
	let finished = 0n;
	let max = 0n;
	for (const row of rows) {
		if (row.terminal) finished += row.count;
		else active += row.count;
		if (row.count > max) max = row.count;
	}

	return { rows, total: active + finished, active, finished, max };
}

/** A bar's share of the largest count, as a percentage from 0 to 100. */
export function barPercent(count: bigint, max: bigint): number {
	if (max <= 0n) return 0;
	return Number((count * 10_000n) / max) / 100;
}
