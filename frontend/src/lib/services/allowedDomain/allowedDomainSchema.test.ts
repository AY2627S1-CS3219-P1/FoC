import { describe, expect, it } from 'vitest';
import { allowedDomainSchema } from './allowedDomainSchema.ts';

describe('allowed domain form schema', () => {
	it.each(['example.com', 'mail.example.com', 'foo-bar.example'])(
		'accepts hostname %s', (domain) => {
			expect(allowedDomainSchema.safeParse({ domain }).success).toBe(true);
		}
	);

	it('trims surrounding whitespace and lowercases the domain', () => {
		expect(allowedDomainSchema.parse({ domain: ' EXAMPLE.com ' })).toEqual({ domain: 'example.com' });
	});

	it.each(['', ' ', 'user@example.com', 'https://example.com', '*.example.com', '-foo.com', 'foo-.com', 'foo..com', 'foo_bar.com', 'example.123', 'example.c', 'localhost', 'intranet', '例子.com', 'example.com.', 'mail.example.com.', ' example.com. '])(
		'rejects invalid hostname %s', (domain) => {
			expect(allowedDomainSchema.safeParse({ domain }).success).toBe(false);
		}
	);

	it('enforces hostname and label length boundaries without a trailing dot', () => {
		const longest = `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`;
		expect(allowedDomainSchema.safeParse({ domain: longest }).success).toBe(true);
		expect(allowedDomainSchema.safeParse({ domain: `${longest}.` }).success).toBe(false);
		expect(allowedDomainSchema.safeParse({ domain: `${longest}d` }).success).toBe(false);
		expect(allowedDomainSchema.safeParse({ domain: `${'a'.repeat(64)}.com` }).success).toBe(false);
	});
});
