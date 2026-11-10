import { z } from 'zod';
import { characterCount } from './moderation.ts';

/** Trimmed text of 1 to `max` characters, counted the way the backend counts them. */
export function boundedText(max: number, subject: string) {
	const limit = max.toLocaleString('en-SG');
	return z
		.string()
		.trim()
		.refine((value) => characterCount(value) >= 1, `Enter ${subject}.`)
		.refine((value) => characterCount(value) <= max, `Use ${limit} characters or fewer.`);
}

export const responseSchema = z.object({
	body: boundedText(4000, 'your response')
});

export type ResponseForm = z.infer<typeof responseSchema>;
