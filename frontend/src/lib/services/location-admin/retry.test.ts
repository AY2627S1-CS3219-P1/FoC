import { describe, expect, it } from 'vitest';
import { CreationAttempt } from './retry';
describe('creation retry identity', () => {
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
});
