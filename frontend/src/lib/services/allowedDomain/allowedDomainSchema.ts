import { z } from 'zod';

const DOMAIN_PATTERN = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

function isDomain(value: string): boolean {
	return value.length > 0 && value.length <= 253 && DOMAIN_PATTERN.test(value);
}

export const allowedDomainSchema = z.object({
	domain: z.string().trim().toLowerCase().min(1, 'Enter a domain.').refine(isDomain, 'Enter a valid domain, such as example.com.')
});
