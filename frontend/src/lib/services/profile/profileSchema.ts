import { isValidPhoneNumber } from 'libphonenumber-js';
import { z } from 'zod';

export const profileSchema = z.object({
	displayName: z.string().trim().min(1, 'Enter a display name.').max(100, 'Use 100 characters or fewer.'),
	description: z.string().max(500, 'Use 500 characters or fewer.'),
	phoneNumber: z.string().trim().min(1, 'Enter a phone number.')
		.refine((value) => isValidPhoneNumber(value, 'SG'), 'Enter a valid phone number.')
});

export type ProfileFormData = z.infer<typeof profileSchema>;
