import { afterEach, describe, expect, it, vi } from 'vitest';
import { CreationAttempt } from './retry';
describe('creation retry identity', () => {
	afterEach(() => vi.useRealTimers());
	it('retains the same key and payload after an uncertain result', () => {
		const attempt = new CreationAttempt<{ name: string }>();
		const payload = { name: 'Shop' },
			first = attempt.prepare(payload);
		payload.name = 'Changed outside';
		expect(first.payload.name).toBe('Shop');
		expect(attempt.prepare({ name: 'Shop' }).key).toBe(first.key);
	});
	it('requires an explicit restart for changed input', () => {
		const attempt = new CreationAttempt<{ name: string }>();
		const first = attempt.prepare({ name: 'Shop' });
		expect(() => attempt.prepare({ name: 'Another' })).toThrow('already have succeeded');
		attempt.clear();
		expect(attempt.pending).toBe(false);
		expect(attempt.prepare({ name: 'Another' }).key).not.toBe(first.key);
	});
	it('blocks an unchanged retry once the backend key lifetime expires', () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2026-10-21T00:00:00Z'));
		const attempt = new CreationAttempt<{ name: string }>();
		const first = attempt.prepare({ name: 'Shop' });
		vi.advanceTimersByTime(24 * 60 * 60 * 1000 - 1);
		expect(attempt.prepare({ name: 'Shop' }).key).toBe(first.key);
		vi.advanceTimersByTime(1);
		expect(() => attempt.prepare({ name: 'Shop' })).toThrow('expired after 24 hours');
		expect(attempt.pending).toBe(true);
		attempt.clear();
		expect(attempt.prepare({ name: 'Shop' }).key).not.toBe(first.key);
	});
});
