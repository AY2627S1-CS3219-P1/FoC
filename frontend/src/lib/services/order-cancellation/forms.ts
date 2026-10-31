import { z } from 'zod';

const boundedText = z.string().trim().refine(value => [...value].length <= 500, 'Use at most 500 characters.');
export const cancellationSchema = z.object({ reason: boundedText });
export const dispositionSchema = z.object({
	location: boundedText.refine(value => value.length > 0, 'Enter the final item location.')
});
