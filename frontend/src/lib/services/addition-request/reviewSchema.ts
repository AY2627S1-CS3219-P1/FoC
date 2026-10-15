import { z } from 'zod';
export const rejectionSchema = z.object({
 reason: z.string().trim().refine((v) => Array.from(v).length >= 1 && Array.from(v).length <= 2000, 'Enter a rejection reason of 1–2,000 characters.')
});
