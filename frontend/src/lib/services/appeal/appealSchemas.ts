import { z } from 'zod';
import { boundedText } from '../moderation/schemas.ts';

export const submitAppealSchema = z.object({
	/** Keys of the chosen decisions, `<kind>:<id>`. */
	decisions: z
		.array(z.string())
		.min(1, 'Choose at least one decision to appeal.')
		.max(10, 'Appeal at most 10 decisions at once.'),
	reason: boundedText(4000, 'why the decision should change')
});

export const closeAppealSchema = z.object({
	resolutions: z
		.array(
			z.object({
				decisionId: z.uuid(),
				outcome: z.enum(['upheld', 'overturned'], 'Choose upheld or overturned.'),
				reason: boundedText(2000, 'a reason for this decision')
			})
		)
		.min(1)
		.max(10)
});

export type SubmitAppealForm = z.infer<typeof submitAppealSchema>;
export type CloseAppealForm = z.infer<typeof closeAppealSchema>;
