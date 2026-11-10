import { describe, expect, it } from 'vitest';
import { closeReportSchema, submitReportSchema } from './reportSchemas.ts';

describe('report form schemas', () => {
	it('requires a category and a description to report', () => {
		expect(submitReportSchema.safeParse({ category: 'fraud', description: 'Took my money' }).success).toBe(true);
		expect(submitReportSchema.safeParse({ category: '', description: 'Took my money' }).success).toBe(false);
		expect(submitReportSchema.safeParse({ category: 'fraud', description: 'x'.repeat(4001) }).success).toBe(false);
	});

	it('allows removing a review only for a review report', () => {
		const close = { outcome: 'review_removal', reason: 'Abusive' };
		expect(closeReportSchema.safeParse({ ...close, reviewTarget: true }).success).toBe(true);
		expect(closeReportSchema.safeParse({ ...close, reviewTarget: false }).success).toBe(false);
	});
});
