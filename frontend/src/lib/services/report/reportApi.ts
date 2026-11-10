import type { EvidenceUploadGrant } from '$lib/gen/moderation/evidence/v1/evidence_pb';
import type { CaseStatus, EvidenceRecord, EvidenceUploadInput, PageQuery, PageResult } from '../moderation/index.ts';

export type ReportCategory = 'harassment' | 'fraud' | 'no_show' | 'inappropriate_content' | 'other';

export const REPORT_CATEGORIES: ReportCategory[] = ['harassment', 'fraud', 'no_show', 'inappropriate_content', 'other'];

export const reportCategoryLabels: Record<ReportCategory, string> = {
	harassment: 'Harassment',
	fraud: 'Fraud',
	no_show: 'No-show',
	inappropriate_content: 'Inappropriate content',
	other: 'Other'
};

export type ReportOutcome = 'no_action' | 'warning' | 'suspension' | 'review_removal';

export const REPORT_OUTCOMES: ReportOutcome[] = ['no_action', 'warning', 'suspension', 'review_removal'];

export const reportOutcomeLabels: Record<ReportOutcome, string> = {
	no_action: 'No action',
	warning: 'Warning',
	suspension: 'Suspension',
	review_removal: 'Review removed'
};

export type ReportTarget = { kind: 'review'; reviewId: string } | { kind: 'order'; orderId: string };

export type ReportRecord = {
	id: string;
	reporterId: string;
	reporterName: string;
	/** The reported user; for a review target, the review's author. */
	subjectId: string;
	subjectName: string;
	target: ReportTarget;
	category: ReportCategory;
	description: string;
	status: CaseStatus;
	/** Set once closed. */
	outcome: ReportOutcome | null;
	outcomeReason: string;
	closedAt: Date | null;
	closedBy: string;
	closerName: string;
	createdAt: Date;
	updatedAt: Date;
	evidence: EvidenceRecord[];
};

export type ReportResponseRecord = {
	id: string;
	responderId: string;
	responderName: string;
	body: string;
	createdAt: Date;
	evidence: EvidenceRecord[];
};

export type ReportDetail = { report: ReportRecord; responses: ReportResponseRecord[] };

export type ReportPage = PageResult & { reports: ReportRecord[] };

export type SubmitReportTarget = { kind: 'review'; reviewId: string } | { kind: 'order'; orderId: string; subjectId: string };

export type SubmitReportInput = {
	target: SubmitReportTarget;
	category: ReportCategory;
	description: string;
	evidenceIds: string[];
};

export type ReportFilter = PageQuery & { status?: CaseStatus; subjectId?: string };

export interface ReportApi {
	// Participants.
	submitReport(input: SubmitReportInput): Promise<ReportRecord>;
	listMyReports(query?: PageQuery): Promise<ReportPage>;
	getReport(id: string): Promise<ReportDetail>;
	submitResponse(reportId: string, body: string, evidenceIds: string[]): Promise<ReportRecord>;
	createEvidenceUpload(input: EvidenceUploadInput, signal: AbortSignal): Promise<EvidenceUploadGrant | undefined>;
	getEvidenceDownload(evidenceId: string): Promise<EvidenceRecord>;

	// Administrators.
	listReports(filter?: ReportFilter): Promise<ReportPage>;
	beginReview(id: string): Promise<ReportRecord>;
	requestResponse(id: string): Promise<ReportRecord>;
	closeReport(id: string, outcome: ReportOutcome, reason: string): Promise<ReportRecord>;
}

/** Whether the caller may appeal a closed report, and which decision to name. */
export function appealableDecision(
	report: ReportRecord,
	callerId: string
): { kind: 'report'; id: string } | { kind: 'retained_review'; id: string } | null {
	if (report.status !== 'closed' || !report.outcome) return null;
	if (report.subjectId === callerId) {
		return report.outcome === 'no_action' ? null : { kind: 'report', id: report.id };
	}
	if (report.reporterId === callerId && report.outcome === 'no_action') {
		// A reporter appeals a kept review as the review, not as the report.
		return report.target.kind === 'review'
			? { kind: 'retained_review', id: report.target.reviewId }
			: { kind: 'report', id: report.id };
	}
	return null;
}
