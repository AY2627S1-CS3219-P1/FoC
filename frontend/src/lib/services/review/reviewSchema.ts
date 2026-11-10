import { z } from 'zod';

// Bounds mirror the ReviewSubmission contract: rating 1..5, comment 1..100000.
export const reviewSchema = z.object({
	rating: z.number('Choose a rating.').int().min(1, 'Choose a rating.').max(5, 'Choose a rating.'),
	comment: z
		.string('Share how the request went.')
		.trim()
		.min(1, 'Share how the request went.')
		.max(100000, 'Use 100000 characters or fewer.')
});

export type ReviewFormData = z.infer<typeof reviewSchema>;
