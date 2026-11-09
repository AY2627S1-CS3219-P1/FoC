import { describe, expect, it, vi } from 'vitest';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import {
	singaporeDate,
	singaporeInput,
	closureSchema,
	closureInput,
	changedClosurePaths
} from './closureSchema';
describe('Singapore closure dates', () => {
	it('converts wall time without relying on the browser timezone', () => {
		expect(singaporeDate('2026-10-15T16:30')?.toISOString()).toBe('2026-10-15T08:30:00.000Z');
		expect(singaporeInput(timestampFromDate(new Date('2026-10-15T08:30:15Z')))).toBe(
			'2026-10-15T16:30:15'
		);
	});
	it('rejects invalid calendar values and nonfinite dates', () => {
		for (const value of ['2026-02-30T10:00', '2026-13-01T10:00', '2026-10-15T25:00', 'invalid'])
			expect(singaporeDate(value)).toBeUndefined();
	});
	it('supports omitted immediate start and indefinite end', () => {
		expect(closureSchema.safeParse({ startsAt: '', endsAt: '', reason: 'Repair' }).success).toBe(
			true
		);
		expect(closureInput({ startsAt: '', endsAt: '', reason: ' Repair ' })).toEqual({
			startsAt: undefined,
			endsAt: undefined,
			reason: 'Repair'
		});
	});
	it('requires end after explicit or immediate start', () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2026-10-15T08:00:00Z'));
		try {
			expect(
				closureSchema.safeParse({ startsAt: '', endsAt: '2026-10-15T15:00', reason: 'Repair' })
					.success
			).toBe(false);
			expect(
				closureSchema.safeParse({
					startsAt: '2026-10-15T16:00',
					endsAt: '2026-10-15T16:00',
					reason: 'Repair'
				}).success
			).toBe(false);
			expect(
				closureSchema.safeParse({
					startsAt: '2026-10-15T16:00',
					endsAt: '2026-10-15T17:00',
					reason: 'Repair'
				}).success
			).toBe(true);
		} finally {
			vi.useRealTimers();
		}
	});
	it('explicitly masks clearing a scheduled natural end', () => {
		expect(
			changedClosurePaths(
				{ startsAt: '2026-10-15T16:00', endsAt: '2026-10-15T17:00', reason: 'Repair' },
				{ startsAt: '2026-10-15T16:00', endsAt: '', reason: 'Repair' }
			)
		).toEqual(['ends_at']);
	});
});
