// Shared by reports and appeals: both move through the same lifecycle, carry
// image evidence and page their lists the same way.
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
import type { Evidence as ProtoEvidence } from '$lib/gen/moderation/evidence/v1/evidence_pb';
import type { PageInfo } from '$lib/gen/pagination/v1/pagination_pb';

/** OPEN -> UNDER_REVIEW <-> AWAITING_RESPONSE -> CLOSED. */
export type CaseStatus = 'open' | 'under_review' | 'awaiting_response' | 'closed';

export const CASE_STATUSES: CaseStatus[] = ['open', 'under_review', 'awaiting_response', 'closed'];

export const caseStatusLabels: Record<CaseStatus, string> = {
	open: 'Open',
	under_review: 'Under review',
	awaiting_response: 'Awaiting response',
	closed: 'Closed'
};

export type EvidenceRecord = {
	id: string;
	/** Short-lived signed GET URL; request a fresh one after expiresAt. */
	url: string;
	contentType: string;
	expiresAt: number;
};

export type PageQuery = { page?: number; pageSize?: number };

export type PageResult = {
	page: number;
	pageSize: number;
	totalItems: number;
	totalPages: number;
};

export const DEFAULT_PAGE_SIZE = 20;

export function toPagination(query: PageQuery = {}): { page: bigint; pageSize: bigint } {
	return { page: BigInt(query.page ?? 1), pageSize: BigInt(query.pageSize ?? DEFAULT_PAGE_SIZE) };
}

export function toPageResult(info: PageInfo | undefined): PageResult {
	if (!info) throw new Error('The response did not include pagination');
	return {
		page: Number(info.page),
		pageSize: Number(info.pageSize),
		totalItems: Number(info.totalItems),
		totalPages: Number(info.totalPages)
	};
}

export function toEvidence(evidence: ProtoEvidence): EvidenceRecord {
	return {
		id: evidence.id,
		url: evidence.url,
		contentType: evidence.contentType,
		expiresAt: evidence.expiresAt ? timestampDate(evidence.expiresAt).getTime() : 0
	};
}

export function requiredDate(timestamp: Timestamp | undefined, what: string): Date {
	if (!timestamp) throw new Error(`The response did not include ${what}`);
	return timestampDate(timestamp);
}

export function optionalDate(timestamp: Timestamp | undefined): Date | null {
	return timestamp ? timestampDate(timestamp) : null;
}

export function required<T>(value: T | undefined, what: string): T {
	if (value === undefined) throw new Error(`The response did not include ${what}`);
	return value;
}

/** Counts user-visible characters, matching the backend's rune limits. */
export function characterCount(value: string): number {
	return Array.from(value).length;
}

/** Maps a generated enum to a domain string union in both directions. */
export function enumCodec<P, D>(pairs: [P, D][], what: string) {
	return {
		fromProto(value: P): D {
			const match = pairs.find(([proto]) => proto === value);
			if (!match) throw new Error(`The response included an unknown ${what}`);
			return match[1];
		},
		toProto(value: D): P {
			const match = pairs.find(([, domain]) => domain === value);
			if (!match) throw new Error(`Unknown ${what} ${String(value)}`);
			return match[0];
		}
	};
}
