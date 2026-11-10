import type { EvidenceUploadGrant } from '$lib/gen/user/evidence/v1/evidence_pb';
import type { CaseStatus, EvidenceRecord, EvidenceUploadInput, PageQuery, PageResult } from '../moderation/index.ts';

/** The kind of decision an appeal challenges. */
export type DecisionKind = 'report' | 'warning' | 'removed_review' | 'retained_review' | 'credit_entry';

export const decisionKindLabels: Record<DecisionKind, string> = {
	report: 'Report outcome',
	warning: 'Account warning',
	removed_review: 'Removed review',
	retained_review: 'Kept review',
	credit_entry: 'Credit adjustment'
};

export type DecisionOutcome = 'upheld' | 'overturned';

export const decisionOutcomeLabels: Record<DecisionOutcome, string> = {
	upheld: 'Upheld',
	overturned: 'Overturned'
};

export type DecisionTarget = { kind: DecisionKind; id: string };

export type DecisionRecord = {
	id: string;
	target: DecisionTarget;
	/** The other participant affected, or empty when there is none. */
	counterpartId: string;
	counterpartName: string;
	/** Set once the appeal closes. */
	outcome: DecisionOutcome | null;
	outcomeReason: string;
};

export type AppealRecord = {
	id: string;
	appellantId: string;
	appellantName: string;
	requestId: string | null;
	reason: string;
	status: CaseStatus;
	/** The participant asked to respond while awaiting a response. */
	awaitedUserId: string;
	decisions: DecisionRecord[];
	closedAt: Date | null;
	closedBy: string;
	closerName: string;
	createdAt: Date;
	updatedAt: Date;
	evidence: EvidenceRecord[];
};

export type AppealResponseRecord = {
	id: string;
	responderId: string;
	responderName: string;
	body: string;
	createdAt: Date;
	evidence: EvidenceRecord[];
};

export type AppealDetail = { appeal: AppealRecord; responses: AppealResponseRecord[] };

export type AppealPage = PageResult & { appeals: AppealRecord[] };

export type SubmitAppealInput = {
	/** Generated once per form; resubmitting it with the same input returns the original appeal. */
	idempotencyKey: string;
	reason: string;
	decisions: DecisionTarget[];
	evidenceIds: string[];
};

export type DecisionResolution = { decisionId: string; outcome: DecisionOutcome; reason: string };

export type AppealFilter = PageQuery & { status?: CaseStatus; appellantId?: string };

export interface AppealApi {
	// Participants.
	submitAppeal(input: SubmitAppealInput): Promise<AppealRecord>;
	listMyAppeals(query?: PageQuery): Promise<AppealPage>;
	getAppeal(id: string): Promise<AppealDetail>;
	submitResponse(appealId: string, body: string, evidenceIds: string[]): Promise<AppealRecord>;
	createEvidenceUpload(input: EvidenceUploadInput, signal: AbortSignal): Promise<EvidenceUploadGrant | undefined>;
	getEvidenceDownload(evidenceId: string): Promise<EvidenceRecord>;

	// Administrators.
	listAppeals(filter?: AppealFilter): Promise<AppealPage>;
	beginReview(id: string): Promise<AppealRecord>;
	requestResponse(id: string, participantId: string): Promise<AppealRecord>;
	closeAppeal(id: string, resolutions: DecisionResolution[]): Promise<AppealRecord>;
}

/** The distinct counterparts an administrator may ask to respond. */
export function counterparts(appeal: AppealRecord): { id: string; name: string }[] {
	const seen = new Map<string, string>();
	for (const decision of appeal.decisions) {
		if (decision.counterpartId && !seen.has(decision.counterpartId)) {
			seen.set(decision.counterpartId, decision.counterpartName);
		}
	}
	return [...seen].map(([id, name]) => ({ id, name }));
}

/** Whether the user filed the appeal or is affected by one of its decisions. */
export function isAppealParticipant(appeal: AppealRecord, userId: string): boolean {
	return appeal.appellantId === userId || appeal.decisions.some((decision) => decision.counterpartId === userId);
}
