import { describe, expect, it } from 'vitest';
import { submitAppealSchema } from './appealSchemas.ts';

describe('appeal form schemas', () => {
	it('requires between one and ten decisions to appeal', () => {
		expect(submitAppealSchema.safeParse({ decisions: ['warning:w'], reason: 'Unfair' }).success).toBe(true);
		expect(submitAppealSchema.safeParse({ decisions: [], reason: 'Unfair' }).success).toBe(false);
		expect(
			submitAppealSchema.safeParse({ decisions: Array.from({ length: 11 }, (_, i) => `warning:${i}`), reason: 'x' })
				.success
		).toBe(false);
	});
});
