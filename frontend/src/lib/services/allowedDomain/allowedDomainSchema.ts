import { z } from 'zod';

function isHostname(value: string): boolean {
	const labels = value.split('.');
	return value.length > 0 && value.length <= 253
		&& labels.every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label))
		&& !/^\d+$/.test(labels[labels.length - 1]);
}

export const allowedDomainSchema = z.object({
	domain: z.string().trim().min(1, 'Enter a domain.').refine(isHostname, 'Enter a valid domain, such as example.com.')
});
