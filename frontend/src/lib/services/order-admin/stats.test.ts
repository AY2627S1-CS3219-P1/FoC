import { describe, expect, it } from 'vitest';
import { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
import { barPercent, summarizeOrderStats } from './stats.ts';

describe('summarizeOrderStats', () => {
	it('labels each status and splits active from finished orders', () => {
		const summary = summarizeOrderStats([
			{ status: OrderStatus.AWAITING_COURIER, count: 4n },
			{ status: OrderStatus.DELIVERING, count: 1n },
			{ status: OrderStatus.COMPLETED, count: 9n },
			{ status: OrderStatus.CANCELLED, count: 0n }
		]);

		expect(summary.rows.map((row) => [row.label, row.terminal])).toEqual([
			['Awaiting courier', false],
			['Delivering', false],
			['Completed', true],
			['Cancelled', true]
		]);
		expect(summary).toMatchObject({ total: 14n, active: 5n, finished: 9n, max: 9n });
	});

	it('handles no orders', () => {
		expect(summarizeOrderStats([])).toMatchObject({ rows: [], total: 0n, active: 0n, finished: 0n, max: 0n });
	});
});

describe('barPercent', () => {
	it('scales a count against the largest count', () => {
		expect(barPercent(9n, 9n)).toBe(100);
		expect(barPercent(3n, 9n)).toBeCloseTo(33.33);
		expect(barPercent(0n, 9n)).toBe(0);
		expect(barPercent(0n, 0n)).toBe(0);
	});
});
