// Review-text moderation reason codes, in the same order as the code-owned Go
// policy. These are the only codes the policy recognizes; anything else is a
// provider error and must never be surfaced as a verdict.
export const MODERATION_REASON_CODES = [
	'HARASSMENT',
	'HATE',
	'THREAT',
	'SEXUAL_CONTENT',
	'SPAM'
] as const;

export type ModerationReasonCode = (typeof MODERATION_REASON_CODES)[number];

// REASON_MESSAGES are the application-owned, user-facing explanations mapped to
// each known reason code. They deliberately avoid exposing the internal rubric,
// the model's reasoning, or the fact that an LLM produced the verdict. They
// mirror the Go `llmpolicy.ReasonMessage` contract, so the frontend and the
// backend must stay in step.
const REASON_MESSAGES: Record<ModerationReasonCode, string> = {
	HARASSMENT: 'This review was removed because it contained harassing content.',
	HATE: 'This review was removed because it contained hateful content.',
	THREAT: 'This review was removed because it contained threats.',
	SEXUAL_CONTENT: 'This review was removed because it contained sexually explicit content.',
	SPAM: 'This review was removed because it looked like spam or advertising.'
};

/** A shared fallback for a code the policy does not recognize. */
export const UNRECOGNIZED_REASON_MESSAGE = 'This review could not be published.';

/** True when `code` is one of the policy's recognized reason codes. */
export function isModerationReasonCode(code: string): code is ModerationReasonCode {
	return (MODERATION_REASON_CODES as readonly string[]).includes(code);
}

/**
 * User-facing explanation for a known reason code. Unknown codes fall back to a
 * neutral message so callers never surface a raw provider code or reason.
 */
export function moderationReasonMessage(code: string): string {
	return REASON_MESSAGES[code as ModerationReasonCode] ?? UNRECOGNIZED_REASON_MESSAGE;
}

/**
 * User-facing explanations for a set of reason codes, in order. Codes the
 * policy does not recognize are still mapped to the neutral fallback so the
 * array never loses position.
 */
export function moderationReasonMessages(codes: readonly string[]): string[] {
	return codes.map((code) => moderationReasonMessage(code));
}
