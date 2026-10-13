import { z } from 'zod';

export const profileSchema = z.object({
	displayName: z.string().trim().min(1, 'Enter a display name.').max(100, 'Use 100 characters or fewer.'),
	description: z.string().max(500, 'Use 500 characters or fewer.'),
	phoneNumber: z.string().trim().min(1, 'Enter a phone number.').max(20, 'Use 20 characters or fewer.')
});

export type ProfileFormData = z.infer<typeof profileSchema>;
