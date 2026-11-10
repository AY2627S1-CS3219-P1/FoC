import { describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import type { Client } from '@connectrpc/connect';
import { PageInfoSchema } from '$lib/gen/pagination/v1/pagination_pb';
import { EvidenceSchema } from '$lib/gen/user/evidence/v1/evidence_pb';
import {
	ReportCategory,
	ReportOutcome,
	ReportSchema,
	ReportService,
	ReportStatus,
	type Report
} from '$lib/gen/user/report/v1/report_pb';
import { ConnectReportApi } from './connectReportApi.ts';
import { appealableDecision, type ReportRecord } from './reportApi.ts';

const createdAt = new Date('2026-11-01T02:00:00Z');
const closedAt = new Date('2026-11-02T02:00:00Z');
const expiresAt = new Date('2026-11-02T03:00:00Z');
const reporter = '00000000-0000-4000-8000-000000000001';
const subject = '00000000-0000-4000-8000-000000000002';

function protoReport(overrides: Partial<Report> = {}): Report {
	const message = create(ReportSchema, {
		id: 'report-id',
		reporterId: reporter,
		reporterName: 'Rae',
		subjectId: subject,
		subjectName: 'Sam',
		target: { case: 'orderId', value: 'order-id' },
		category: ReportCategory.NO_SHOW,
		description: 'Never arrived',
		status: ReportStatus.OPEN,
		createdAt: timestampFromDate(createdAt),
		updatedAt: timestampFromDate(createdAt),
		evidence: [
			create(EvidenceSchema, {
				id: 'evidence-id',
				url: 'https://storage.test/e',
				contentType: 'image/png',
				expiresAt: timestampFromDate(expiresAt)
			})
		],
	});
	return Object.assign(message, overrides);
}

function clientWith(methods: Record<string, unknown>) {
	return methods as unknown as Client<typeof ReportService>;
}

describe('ConnectReportApi', () => {
	it('maps a report to plain domain values', async () => {
		const client = clientWith({ getReport: vi.fn(async () => ({ report: protoReport(), responses: [] })) });
		const detail = await new ConnectReportApi(client).getReport('report-id');

		expect(detail.report).toMatchObject({
			id: 'report-id',
			target: { kind: 'order', orderId: 'order-id' },
			category: 'no_show',
			status: 'open',
			outcome: null,
			closedAt: null,
			createdAt,
			evidence: [{ id: 'evidence-id', url: 'https://storage.test/e', contentType: 'image/png', expiresAt: expiresAt.getTime() }]
		});
	});

	it('maps the outcome of a closed report', async () => {
		const closed = protoReport({
			status: ReportStatus.CLOSED,
			outcome: ReportOutcome.REVIEW_REMOVAL,
			outcomeReason: 'Abusive',
			closedAt: timestampFromDate(closedAt),
			target: { case: 'reviewId', value: 'review-id' }
		});
		const client = clientWith({ getReport: vi.fn(async () => ({ report: closed, responses: [] })) });
		const { report } = await new ConnectReportApi(client).getReport('report-id');

		expect(report).toMatchObject({
			status: 'closed',
			outcome: 'review_removal',
			outcomeReason: 'Abusive',
			closedAt,
			target: { kind: 'review', reviewId: 'review-id' }
		});
	});

	it('submits an order report with its subject and evidence', async () => {
		const submitReport = vi.fn(async () => ({ report: protoReport() }));
		await new ConnectReportApi(clientWith({ submitReport })).submitReport({
			target: { kind: 'order', orderId: 'order-id', subjectId: subject },
			category: 'fraud',
			description: 'Paid but never delivered',
			evidenceIds: ['e1', 'e2']
		});

		expect(submitReport).toHaveBeenCalledWith({
			target: { case: 'order', value: { orderId: 'order-id', subjectId: subject } },
			category: ReportCategory.FRAUD,
			description: 'Paid but never delivered',
			evidenceIds: ['e1', 'e2']
		});
	});

	it('filters the admin queue and converts pagination', async () => {
		const listReports = vi.fn(async () => ({
			reports: [protoReport()],
			pagination: create(PageInfoSchema, { page: 2n, pageSize: 20n, totalItems: 21n, totalPages: 2n })
		}));
		const page = await new ConnectReportApi(clientWith({ listReports })).listReports({ status: 'awaiting_response', page: 2 });

		expect(listReports).toHaveBeenCalledWith({
			status: ReportStatus.AWAITING_RESPONSE,
			subjectId: undefined,
			pagination: { page: 2n, pageSize: 20n }
		});
		expect(page).toMatchObject({ page: 2, pageSize: 20, totalItems: 21, totalPages: 2 });
		expect(page.reports).toHaveLength(1);
	});

	it('closes a report with a domain outcome', async () => {
		const closeReport = vi.fn(async () => ({ report: protoReport() }));
		await new ConnectReportApi(clientWith({ closeReport })).closeReport('report-id', 'warning', 'Rude');

		expect(closeReport).toHaveBeenCalledWith({ id: 'report-id', outcome: ReportOutcome.WARNING, reason: 'Rude' });
	});

	it('rejects incomplete responses', async () => {
		const client = clientWith({ beginReview: vi.fn(async () => ({})) });
		await expect(new ConnectReportApi(client).beginReview('report-id')).rejects.toThrow('did not include the report');
	});

	it('rejects a report without a target', async () => {
		const client = clientWith({
			getReport: vi.fn(async () => ({ report: protoReport({ target: { case: undefined } }), responses: [] }))
		});
		await expect(new ConnectReportApi(client).getReport('report-id')).rejects.toThrow('target');
	});
});

describe('appealableDecision', () => {
	const base: ReportRecord = {
		id: 'report-id',
		reporterId: reporter,
		reporterName: 'Rae',
		subjectId: subject,
		subjectName: 'Sam',
		target: { kind: 'order', orderId: 'order-id' },
		category: 'other',
		description: 'x',
		status: 'closed',
		outcome: 'warning',
		outcomeReason: 'r',
		closedAt,
		closedBy: '',
		closerName: '',
		createdAt,
		updatedAt: createdAt,
		evidence: []
	};

	it('lets the subject appeal a consequence', () => {
		expect(appealableDecision(base, subject)).toEqual({ kind: 'report', id: 'report-id' });
		expect(appealableDecision(base, reporter)).toBeNull();
	});

	it('lets the reporter appeal a no-action order report', () => {
		const report = { ...base, outcome: 'no_action' as const };
		expect(appealableDecision(report, reporter)).toEqual({ kind: 'report', id: 'report-id' });
		expect(appealableDecision(report, subject)).toBeNull();
	});

	it('names the kept review for a no-action review report', () => {
		const report = { ...base, outcome: 'no_action' as const, target: { kind: 'review' as const, reviewId: 'review-id' } };
		expect(appealableDecision(report, reporter)).toEqual({ kind: 'retained_review', id: 'review-id' });
	});

	it('offers nothing before the report closes', () => {
		expect(appealableDecision({ ...base, status: 'under_review', outcome: null }, subject)).toBeNull();
	});
});
