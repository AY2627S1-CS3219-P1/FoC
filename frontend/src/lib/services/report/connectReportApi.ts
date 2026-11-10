import type { Client } from '@connectrpc/connect';
import {
	ReportCategory as ProtoCategory,
	ReportOutcome as ProtoOutcome,
	ReportService,
	ReportStatus as ProtoStatus,
	type Report as ProtoReport,
	type ReportResponse as ProtoReportResponse
} from '$lib/gen/user/report/v1/report_pb';
import type { EvidenceUploadGrant } from '$lib/gen/user/evidence/v1/evidence_pb';
import {
	enumCodec,
	optionalDate,
	required,
	requiredDate,
	toEvidence,
	toPagination,
	toPageResult,
	type CaseStatus,
	type EvidenceRecord,
	type EvidenceUploadInput,
	type PageQuery
} from '../moderation/index.ts';
import type {
	ReportApi,
	ReportCategory,
	ReportDetail,
	ReportFilter,
	ReportOutcome,
	ReportPage,
	ReportRecord,
	ReportResponseRecord,
	ReportTarget,
	SubmitReportInput
} from './reportApi.ts';

type ReportClient = Client<typeof ReportService>;

const categories = enumCodec<ProtoCategory, ReportCategory>(
	[
		[ProtoCategory.HARASSMENT, 'harassment'],
		[ProtoCategory.FRAUD, 'fraud'],
		[ProtoCategory.NO_SHOW, 'no_show'],
		[ProtoCategory.INAPPROPRIATE_CONTENT, 'inappropriate_content'],
		[ProtoCategory.OTHER, 'other']
	],
	'report category'
);

const statuses = enumCodec<ProtoStatus, CaseStatus>(
	[
		[ProtoStatus.OPEN, 'open'],
		[ProtoStatus.UNDER_REVIEW, 'under_review'],
		[ProtoStatus.AWAITING_RESPONSE, 'awaiting_response'],
		[ProtoStatus.CLOSED, 'closed']
	],
	'report status'
);

const outcomes = enumCodec<ProtoOutcome, ReportOutcome>(
	[
		[ProtoOutcome.NO_ACTION, 'no_action'],
		[ProtoOutcome.WARNING, 'warning'],
		[ProtoOutcome.SUSPENSION, 'suspension'],
		[ProtoOutcome.REVIEW_REMOVAL, 'review_removal']
	],
	'report outcome'
);

function toTarget(report: ProtoReport): ReportTarget {
	switch (report.target.case) {
		case 'reviewId':
			return { kind: 'review', reviewId: report.target.value };
		case 'orderId':
			return { kind: 'order', orderId: report.target.value };
		default:
			throw new Error('The report response did not include a target');
	}
}

export function toReport(report: ProtoReport): ReportRecord {
	const closed = report.status === ProtoStatus.CLOSED;
	return {
		id: report.id,
		reporterId: report.reporterId,
		reporterName: report.reporterName,
		subjectId: report.subjectId,
		subjectName: report.subjectName,
		target: toTarget(report),
		category: categories.fromProto(report.category),
		description: report.description,
		status: statuses.fromProto(report.status),
		outcome: closed ? outcomes.fromProto(report.outcome) : null,
		outcomeReason: report.outcomeReason,
		closedAt: optionalDate(report.closedAt),
		closedBy: report.closedBy,
		closerName: report.closerName,
		createdAt: requiredDate(report.createdAt, 'a creation time'),
		updatedAt: requiredDate(report.updatedAt, 'an update time'),
		evidence: report.evidence.map(toEvidence)
	};
}

function toResponse(response: ProtoReportResponse): ReportResponseRecord {
	return {
		id: response.id,
		responderId: response.responderId,
		responderName: response.responderName,
		body: response.body,
		createdAt: requiredDate(response.createdAt, 'a response time'),
		evidence: response.evidence.map(toEvidence)
	};
}

export class ConnectReportApi implements ReportApi {
	readonly #client: ReportClient;

	constructor(client: ReportClient) {
		this.#client = client;
	}

	// Participants

	async submitReport(input: SubmitReportInput): Promise<ReportRecord> {
		const target =
			input.target.kind === 'review'
				? { case: 'reviewId' as const, value: input.target.reviewId }
				: {
						case: 'order' as const,
						value: { orderId: input.target.orderId, subjectId: input.target.subjectId }
					};
		const response = await this.#client.submitReport({
			target,
			category: categories.toProto(input.category),
			description: input.description,
			evidenceIds: input.evidenceIds
		});
		return toReport(required(response.report, 'the report'));
	}

	async listMyReports(query?: PageQuery): Promise<ReportPage> {
		const response = await this.#client.listMyReports({ pagination: toPagination(query) });
		return { reports: response.reports.map(toReport), ...toPageResult(response.pagination) };
	}

	async getReport(id: string): Promise<ReportDetail> {
		const response = await this.#client.getReport({ id });
		return {
			report: toReport(required(response.report, 'the report')),
			responses: response.responses.map(toResponse)
		};
	}

	async submitResponse(reportId: string, body: string, evidenceIds: string[]): Promise<ReportRecord> {
		const response = await this.#client.submitResponse({ reportId, body, evidenceIds });
		return toReport(required(response.report, 'the report'));
	}

	async createEvidenceUpload(
		input: EvidenceUploadInput,
		signal: AbortSignal
	): Promise<EvidenceUploadGrant | undefined> {
		const response = await this.#client.createEvidenceUpload(
			{ upload: { contentType: input.contentType, sizeBytes: BigInt(input.sizeBytes) } },
			{ signal, timeoutMs: 15000 }
		);
		return response.grant;
	}

	async getEvidenceDownload(evidenceId: string): Promise<EvidenceRecord> {
		const response = await this.#client.getEvidenceDownload({ evidenceId });
		return toEvidence(required(response.evidence, 'the evidence'));
	}

	// Administrators

	async listReports(filter: ReportFilter = {}): Promise<ReportPage> {
		const response = await this.#client.listReports({
			status: filter.status ? statuses.toProto(filter.status) : undefined,
			subjectId: filter.subjectId || undefined,
			pagination: toPagination(filter)
		});
		return { reports: response.reports.map(toReport), ...toPageResult(response.pagination) };
	}

	async beginReview(id: string): Promise<ReportRecord> {
		const response = await this.#client.beginReview({ id });
		return toReport(required(response.report, 'the report'));
	}

	async requestResponse(id: string): Promise<ReportRecord> {
		const response = await this.#client.requestResponse({ id });
		return toReport(required(response.report, 'the report'));
	}

	async closeReport(id: string, outcome: ReportOutcome, reason: string): Promise<ReportRecord> {
		const response = await this.#client.closeReport({ id, outcome: outcomes.toProto(outcome), reason });
		return toReport(required(response.report, 'the report'));
	}
}
