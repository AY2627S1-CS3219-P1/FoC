import { describe, expect, it } from 'vitest';
import { profileSchema } from './profileSchema.ts';

describe('profileSchema', () => {
	it('trims the display name and optional contacts', () => {
		expect(profileSchema.parse({
			displayName: '  Casey  ', description: 'A short bio', telegramHandle: '  casey_1  ', phoneNumber: ' 123 '
		})).toEqual({
			displayName: 'Casey', description: 'A short bio', telegramHandle: 'casey_1', phoneNumber: '123'
		});
	});

	it('accepts empty optional contact fields for clearing them', () => {
		expect(profileSchema.safeParse({ displayName: 'Casey', description: '', telegramHandle: '', phoneNumber: '' }).success).toBe(true);
	});

	it('rejects invalid names, descriptions, and Telegram handles', () => {
		expect(profileSchema.safeParse({ displayName: '  ', description: '', telegramHandle: '', phoneNumber: '' }).success).toBe(false);
		expect(profileSchema.safeParse({ displayName: 'Casey', description: 'x'.repeat(501), telegramHandle: '', phoneNumber: '' }).success).toBe(false);
		expect(profileSchema.safeParse({ displayName: 'Casey', description: '', telegramHandle: 'ab@cd', phoneNumber: '' }).success).toBe(false);
	});
});
