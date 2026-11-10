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
	HARASSMENT: 'It contains harassing content.',
	HATE: 'It contains hateful content.',
	THREAT: 'It contains threats.',
	SEXUAL_CONTENT: 'It contains sexual content.',
	SPAM: 'It looks like spam or advertising.'
};

export const UNRECOGNIZED_REASON_MESSAGE = 'This review can\u2019t be published.';

export function isModerationReasonCode(code: string): code is ModerationReasonCode {
	return (MODERATION_REASON_CODES as readonly string[]).includes(code);
}

export function moderationReasonMessage(code: string): string {
	return REASON_MESSAGES[code as ModerationReasonCode] ?? UNRECOGNIZED_REASON_MESSAGE;
}
