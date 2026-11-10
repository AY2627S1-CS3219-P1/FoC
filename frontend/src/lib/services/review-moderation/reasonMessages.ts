// Keep reason codes aligned with internal/moderation/review_rules.go.
export const MODERATION_REASON_CODES = [
	'HARASSMENT',
	'HATE',
	'THREAT',
	'SEXUAL_CONTENT',
	'SPAM'
] as const;

export type ModerationReasonCode = (typeof MODERATION_REASON_CODES)[number];


const REASON_MESSAGES: Record<ModerationReasonCode, string> = {
	HARASSMENT: 'This review was removed because it contained harassing content.',
	HATE: 'This review was removed because it contained hateful content.',
	THREAT: 'This review was removed because it contained threats.',
	SEXUAL_CONTENT: 'This review was removed because it contained sexually explicit content.',
	SPAM: 'This review was removed because it looked like spam or advertising.'
};

export const UNRECOGNIZED_REASON_MESSAGE = 'This review could not be published.';

export function isModerationReasonCode(code: string): code is ModerationReasonCode {
	return (MODERATION_REASON_CODES as readonly string[]).includes(code);
}

export function moderationReasonMessage(code: string): string {
	return REASON_MESSAGES[code as ModerationReasonCode] ?? UNRECOGNIZED_REASON_MESSAGE;
}
