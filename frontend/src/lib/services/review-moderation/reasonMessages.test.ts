import { describe, expect, it } from 'vitest';
import {
	MODERATION_REASON_CODES,
	isModerationReasonCode,
	moderationReasonMessage,
	UNRECOGNIZED_REASON_MESSAGE
} from './reasonMessages.ts';

describe('review moderation reason message mirror', () => {
	it('mirrors each code to its application-owned message', () => {
		expect(moderationReasonMessage('HARASSMENT')).toBe(
			'This review was removed because it contained harassing content.'
		);
		expect(moderationReasonMessage('HATE')).toBe(
			'This review was removed because it contained hateful content.'
		);
		expect(moderationReasonMessage('THREAT')).toBe(
			'This review was removed because it contained threats.'
		);
		expect(moderationReasonMessage('SEXUAL_CONTENT')).toBe(
			'This review was removed because it contained sexually explicit content.'
		);
		expect(moderationReasonMessage('SPAM')).toBe(
			'This review was removed because it looked like spam or advertising.'
		);
	});

	it('treats the five codes as the complete recognized set', () => {
		expect([...MODERATION_REASON_CODES]).toHaveLength(5);
		for (const code of MODERATION_REASON_CODES) {
			expect(isModerationReasonCode(code)).toBe(true);
		}
		expect(isModerationReasonCode('SPAMMY')).toBe(false);
		expect(isModerationReasonCode('')).toBe(false);
	});

	it('maps an unrecognized code to the neutral fallback', () => {
		expect(moderationReasonMessage('TOSLICK')).toBe(UNRECOGNIZED_REASON_MESSAGE);
		expect(isModerationReasonCode('TOSLICK')).toBe(false);
	});

});
