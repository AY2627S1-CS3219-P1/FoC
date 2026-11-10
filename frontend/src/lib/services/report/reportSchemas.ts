import { z } from 'zod';
import { boundedText } from '../moderation/schemas.ts';
import { REPORT_CATEGORIES, REPORT_OUTCOMES } from './reportApi.ts';

export const submitReportSchema = z.object({
	category: z.enum(REPORT_CATEGORIES, 'Choose a category.'),
	description: boundedText(4000, 'what happened')
});

export const closeReportSchema = z
	.object({
		outcome: z.enum(REPORT_OUTCOMES, 'Choose an outcome.'),
		reason: boundedText(2000, 'a reason both participants will see'),
		/** Only a review report can remove the review. */
		reviewTarget: z.boolean()
	})
	.superRefine((value, ctx) => {
		if (value.outcome === 'review_removal' && !value.reviewTarget) {
			ctx.addIssue({ code: 'custom', path: ['outcome'], message: 'Only a review report can remove the review.' });
		}
	});

export type SubmitReportForm = z.infer<typeof submitReportSchema>;
export type CloseReportForm = z.infer<typeof closeReportSchema>;
