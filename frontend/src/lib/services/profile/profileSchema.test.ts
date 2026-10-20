import { describe, expect, it } from 'vitest';
import { profileSchema } from './profileSchema.ts';

describe('profileSchema', () => {
	it('trims the display name and phone number', () => {
		expect(profileSchema.parse({
			displayName: '  Casey  ', description: 'A short bio', phoneNumber: ' +6591234567 '
		})).toEqual({
			displayName: 'Casey', description: 'A short bio', phoneNumber: '+6591234567'
		});
	});

	it('requires a valid phone number', () => {
		for (const phoneNumber of ['', '   ', '12345', '+6512']) {
			expect(profileSchema.safeParse({ displayName: 'Casey', description: '', phoneNumber }).success).toBe(false);
		}
	});

	it('accepts Singapore numbers without a country code and foreign numbers', () => {
		for (const phoneNumber of ['91234567', '+6581234567', '+447911123456']) {
			expect(profileSchema.safeParse({ displayName: 'Casey', description: '', phoneNumber }).success).toBe(true);
		}
	});

	it('rejects invalid names and descriptions', () => {
		expect(profileSchema.safeParse({ displayName: '  ', description: '', phoneNumber: '+6591234567' }).success).toBe(false);
		expect(profileSchema.safeParse({ displayName: 'Casey', description: 'x'.repeat(501), phoneNumber: '+6591234567' }).success).toBe(false);
	});
});
