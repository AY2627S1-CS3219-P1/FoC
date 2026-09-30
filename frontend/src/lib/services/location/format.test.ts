import { describe, expect, it } from 'vitest';
import { formatOpeningHours, formatTimeOfDay } from './format.ts';

describe('formatTimeOfDay', () => {
	it('pads hours and minutes to two digits', () => {
		expect(formatTimeOfDay({ hours: 9, minutes: 5 })).toBe('09:05');
		expect(formatTimeOfDay({ hours: 22, minutes: 30 })).toBe('22:30');
	});
});

describe('formatOpeningHours', () => {
	it('shows a daytime range', () => {
		expect(formatOpeningHours({ hours: 9, minutes: 0 }, { hours: 18, minutes: 0 })).toBe(
			'09:00–18:00'
		);
	});

	it('marks ranges that close after midnight', () => {
		expect(formatOpeningHours({ hours: 22, minutes: 0 }, { hours: 2, minutes: 0 })).toBe(
			'22:00–02:00 (overnight)'
		);
	});

	it('says when hours are not listed', () => {
		expect(formatOpeningHours(undefined, undefined)).toBe('Hours not listed');
	});
});
