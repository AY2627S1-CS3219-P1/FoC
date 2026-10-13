import { describe, expect, it } from 'vitest';
import { profileSchema } from './profileSchema.ts';

describe('profileSchema', () => {
	it('trims the display name and phone number', () => {
		expect(profileSchema.parse({
			displayName: '  Casey  ', description: 'A short bio', phoneNumber: ' +65 9123 4567 '
		})).toEqual({
			displayName: 'Casey', description: 'A short bio', phoneNumber: '+65 9123 4567'
		});
	});

	it('requires a phone number', () => {
		expect(profileSchema.safeParse({ displayName: 'Casey', description: '', phoneNumber: '' }).success).toBe(false);
		expect(profileSchema.safeParse({ displayName: 'Casey', description: '', phoneNumber: '   ' }).success).toBe(false);
	});

	it('rejects invalid names, descriptions, and phone numbers', () => {
		expect(profileSchema.safeParse({ displayName: '  ', description: '', phoneNumber: '123' }).success).toBe(false);
		expect(profileSchema.safeParse({ displayName: 'Casey', description: 'x'.repeat(501), phoneNumber: '123' }).success).toBe(false);
		expect(profileSchema.safeParse({ displayName: 'Casey', description: '', phoneNumber: '1'.repeat(21) }).success).toBe(false);
	});
});
