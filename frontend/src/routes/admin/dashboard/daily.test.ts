import { describe, expect, it } from 'vitest';
import { formatAverage, niceCeiling, summarizeDaily } from './daily.ts';

const day = (value: bigint, dayOfMonth: number) => ({
	date: { year: 2026, month: 11, day: dayOfMonth },
	value
});

describe('summarizeDaily', () => {
	it('totals, averages and finds the latest busiest day', () => {
		const points = [day(3n, 1), day(0n, 2), day(7n, 3), day(7n, 4)];

		expect(summarizeDaily(points)).toEqual({
			total: 17n,
			average: 4.25,
			busiest: day(7n, 4),
			scaleMax: 10n,
			ticks: [0, 1, 3]
		});
	});

	it('has no busiest day or scale when every day is empty', () => {
		const summary = summarizeDaily([day(0n, 1), day(0n, 2)]);

		expect(summary.busiest).toBeUndefined();
		expect(summary.scaleMax).toBe(0n);
		expect(summary.ticks).toEqual([0, 1]);
	});

	it('labels a single day once', () => {
		expect(summarizeDaily([day(2n, 1)]).ticks).toEqual([0]);
		expect(summarizeDaily([]).ticks).toEqual([]);
	});
});

describe('niceCeiling', () => {
	it('rounds up to 1, 2 or 5 times a power of ten', () => {
		expect([0n, 1n, 3n, 7n, 10n, 11n, 240n, 5001n].map(niceCeiling)).toEqual([
			0n,
			1n,
			5n,
			10n,
			10n,
			20n,
			500n,
			10000n
		]);
	});
});

describe('formatAverage', () => {
	it('keeps at most one decimal place', () => {
		expect(formatAverage(4.25)).toBe('4.3');
		expect(formatAverage(1200)).toBe('1,200');
	});
});
