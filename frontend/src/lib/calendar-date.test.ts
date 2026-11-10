import { describe, expect, it } from 'vitest';
import {
	addDays,
	dateKey,
	formatLongDate,
	formatShortDate,
	lastDays,
	singaporeToday
} from './calendar-date.ts';

describe('singaporeToday', () => {
	it('uses the Singapore date, eight hours ahead of UTC', () => {
		expect(singaporeToday(new Date('2026-11-09T15:59:00Z'))).toEqual({
			year: 2026,
			month: 11,
			day: 9
		});
		expect(singaporeToday(new Date('2026-11-09T16:00:00Z'))).toEqual({
			year: 2026,
			month: 11,
			day: 10
		});
	});
});

describe('addDays and lastDays', () => {
	it('crosses month and year boundaries', () => {
		const lastOfFebruary = { year: 2026, month: 2, day: 28 };
		expect(addDays({ year: 2026, month: 3, day: 1 }, -1)).toEqual(lastOfFebruary);
		expect(addDays({ year: 2026, month: 12, day: 31 }, 1)).toEqual({ year: 2027, month: 1, day: 1 });
	});

	it('includes the end day in the range', () => {
		const end = { year: 2026, month: 11, day: 10 };
		expect(lastDays(7, end)).toEqual({ start: { year: 2026, month: 11, day: 4 }, end });
		expect(lastDays(1, end)).toEqual({ start: end, end });
	});
});

describe('formatting', () => {
	const date = { year: 2026, month: 11, day: 9 };

	it('keys and labels a date without shifting its day', () => {
		expect(dateKey(date)).toBe('2026-11-09');
		expect(formatShortDate(date)).toBe('9 Nov');
		expect(formatLongDate(date)).toBe('Mon, 9 Nov 2026');
	});
});
