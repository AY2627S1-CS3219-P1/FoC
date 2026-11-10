import type { Client } from '@connectrpc/connect';
import type { MessageInitShape } from '@bufbuild/protobuf';
import {
	AppealService,
	type AppealedDecisionSchema,
	AppealStatus as ProtoStatus,
	DecisionOutcome as ProtoOutcome,
	type Appeal as ProtoAppeal,
	type AppealResponse as ProtoAppealResponse,
	type Decision as ProtoDecision
} from '$lib/gen/moderation/appeal/v1/appeal_pb';
import type { EvidenceUploadGrant } from '$lib/gen/moderation/evidence/v1/evidence_pb';
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
	AppealApi,
	AppealDetail,
	AppealFilter,
	AppealPage,
	AppealRecord,
	AppealResponseRecord,
	DecisionOutcome,
	DecisionRecord,
	DecisionResolution,
	DecisionTarget,
	SubmitAppealInput
} from './appealApi.ts';

type AppealClient = Client<typeof AppealService>;

const statuses = enumCodec<ProtoStatus, CaseStatus>(
	[
		[ProtoStatus.OPEN, 'open'],
		[ProtoStatus.UNDER_REVIEW, 'under_review'],
		[ProtoStatus.AWAITING_RESPONSE, 'awaiting_response'],
		[ProtoStatus.CLOSED, 'closed']
	],
	'appeal status'
);

const outcomes = enumCodec<ProtoOutcome, DecisionOutcome>(
	[
		[ProtoOutcome.UPHELD, 'upheld'],
		[ProtoOutcome.OVERTURNED, 'overturned']
	],
	'decision outcome'
);

function toTarget(target: ProtoDecision['target']): DecisionTarget {
	switch (target.case) {
		case 'reportId':
			return { kind: 'report', id: target.value };
		case 'warningId':
			return { kind: 'warning', id: target.value };
		case 'removedReviewId':
			return { kind: 'removed_review', id: target.value };
		case 'retainedReviewId':
			return { kind: 'retained_review', id: target.value };
		case 'creditEntryId':
			return { kind: 'credit_entry', id: target.value };
		default:
			throw new Error('The appeal response included a decision without a target');
	}
}

function fromTarget(target: DecisionTarget): MessageInitShape<typeof AppealedDecisionSchema> {
	const value = target.id;
	switch (target.kind) {
		case 'report':
			return { target: { case: 'reportId', value } };
		case 'warning':
			return { target: { case: 'warningId', value } };
		case 'removed_review':
			return { target: { case: 'removedReviewId', value } };
		case 'retained_review':
			return { target: { case: 'retainedReviewId', value } };
		case 'credit_entry':
			return { target: { case: 'creditEntryId', value } };
	}
}

function toDecision(decision: ProtoDecision, closed: boolean): DecisionRecord {
	return {
		id: decision.id,
		target: toTarget(decision.target),
		counterpartId: decision.counterpartId,
		counterpartName: decision.counterpartName,
		outcome: closed ? outcomes.fromProto(decision.outcome) : null,
		outcomeReason: decision.outcomeReason
	};
}

export function toAppeal(appeal: ProtoAppeal): AppealRecord {
	const closed = appeal.status === ProtoStatus.CLOSED;
	return {
		id: appeal.id,
		appellantId: appeal.appellantId,
		appellantName: appeal.appellantName,
		requestId: appeal.requestId || null,
		reason: appeal.reason,
		status: statuses.fromProto(appeal.status),
		awaitedUserId: appeal.awaitedUserId,
		decisions: appeal.decisions.map((decision) => toDecision(decision, closed)),
		closedAt: optionalDate(appeal.closedAt),
		closedBy: appeal.closedBy,
		closerName: appeal.closerName,
		createdAt: requiredDate(appeal.createdAt, 'a creation time'),
		updatedAt: requiredDate(appeal.updatedAt, 'an update time'),
		evidence: appeal.evidence.map(toEvidence)
	};
}

function toResponse(response: ProtoAppealResponse): AppealResponseRecord {
	return {
		id: response.id,
		responderId: response.responderId,
		responderName: response.responderName,
		body: response.body,
		createdAt: requiredDate(response.createdAt, 'a response time'),
		evidence: response.evidence.map(toEvidence)
	};
}

export class ConnectAppealApi implements AppealApi {
	readonly #client: AppealClient;

	constructor(client: AppealClient) {
		this.#client = client;
	}

	// Participants

	async submitAppeal(input: SubmitAppealInput): Promise<AppealRecord> {
		const response = await this.#client.submitAppeal({
			idempotencyKey: input.idempotencyKey,
			reason: input.reason,
			decisions: input.decisions.map(fromTarget),
			evidenceIds: input.evidenceIds
		});
		return toAppeal(required(response.appeal, 'the appeal'));
	}

	async listMyAppeals(query?: PageQuery): Promise<AppealPage> {
		const response = await this.#client.listMyAppeals({ pagination: toPagination(query) });
		return { appeals: response.appeals.map(toAppeal), ...toPageResult(response.pagination) };
	}

	async getAppeal(id: string): Promise<AppealDetail> {
		const response = await this.#client.getAppeal({ id });
		return {
			appeal: toAppeal(required(response.appeal, 'the appeal')),
			responses: response.responses.map(toResponse)
		};
	}

	async submitResponse(appealId: string, body: string, evidenceIds: string[]): Promise<AppealRecord> {
		const response = await this.#client.submitResponse({ appealId, body, evidenceIds });
		return toAppeal(required(response.appeal, 'the appeal'));
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

	async listAppeals(filter: AppealFilter = {}): Promise<AppealPage> {
		const response = await this.#client.listAppeals({
			status: filter.status ? statuses.toProto(filter.status) : undefined,
			appellantId: filter.appellantId || undefined,
			pagination: toPagination(filter)
		});
		return { appeals: response.appeals.map(toAppeal), ...toPageResult(response.pagination) };
	}

	async beginReview(id: string): Promise<AppealRecord> {
		const response = await this.#client.beginReview({ id });
		return toAppeal(required(response.appeal, 'the appeal'));
	}

	async requestResponse(id: string, participantId: string): Promise<AppealRecord> {
		const response = await this.#client.requestResponse({ id, participantId });
		return toAppeal(required(response.appeal, 'the appeal'));
	}

	async closeAppeal(id: string, resolutions: DecisionResolution[]): Promise<AppealRecord> {
		const response = await this.#client.closeAppeal({
			id,
			resolutions: resolutions.map((resolution) => ({
				decisionId: resolution.decisionId,
				outcome: outcomes.toProto(resolution.outcome),
				reason: resolution.reason
			}))
		});
		return toAppeal(required(response.appeal, 'the appeal'));
	}
}
