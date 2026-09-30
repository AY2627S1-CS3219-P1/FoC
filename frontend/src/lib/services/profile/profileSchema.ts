import { z } from 'zod';

export const profileSchema = z.object({
	displayName: z.string().trim().min(1, 'Enter a display name.').max(100, 'Use 100 characters or fewer.'),
	description: z.string().max(500, 'Use 500 characters or fewer.'),
	telegramHandle: z.string().trim().max(32, 'Use 32 characters or fewer.')
		.refine((value) => value === '' || /^[A-Za-z0-9_]{5,32}$/.test(value), 'Use 5–32 letters, numbers, or underscores.'),
	phoneNumber: z.string().trim().max(20, 'Use 20 characters or fewer.')
});

export type ProfileFormData = z.infer<typeof profileSchema>;
