import { describe, expect, it } from 'vitest';
import { Code, ConnectError, createClient, createRouterTransport } from '@connectrpc/connect';
import {
	ModerationStatus,
	ReviewService as ReviewServiceDefinition,
	type UnpublishedReview
} from '$lib/gen/user/review/v1/review_pb';
import { create } from '@bufbuild/protobuf';
import { PaginationSchema, PageInfoSchema } from '$lib/gen/pagination/v1/pagination_pb';import {
	ModerationJobStatus,
	ModerationOutcomeType,
	ReviewModerationService as ReviewModerationServiceDefinition,
	type ReviewModerationJobSummary
} from '$lib/gen/moderation/review/v1/review_moderation_pb';
import { MAX_MODERATION_REQUEST_IDS_PER_BATCH } from './reviewModerationApi.ts';
import { ConnectReviewModerationApi } from './connectReviewModerationApi.ts';

function review(id: string, overrides: Partial<UnpublishedReview> = {}): UnpublishedReview {
	return {
		id,
		reviewerId: 'rev-1',
		revieweeId: 'rev-2',
		orderId: 'order-1',
		rating: 3,
		comment: 'The product broke after a week.',
		reviewerName: 'Alice',
		revieweeName: 'Acme',
		moderationStatus: ModerationStatus.PENDING_MODERATION,
		moderationRequestId: id,
		contentRevision: 1,
		moderationReasonCodes: [],
		...overrides
	} as UnpublishedReview;
}

function job(moderationRequestId: string, overrides: Partial<ReviewModerationJobSummary> = {}) {
	return {
		jobId: `job-${moderationRequestId}`,
		moderationRequestId,
		reviewId: `rev-${moderationRequestId}`,
		status: ModerationJobStatus.COMPLETED,
		attemptCount: 1,
		runGeneration: 1,
		outcome: ModerationOutcomeType.APPROVED,
		reasonCodes: [],
		policyVersion: 'review-text-v1',
		...overrides
	} as ReviewModerationJobSummary;
}

function makeApi(
	reviews: UnpublishedReview[],
	jobs: ReviewModerationJobSummary[],
	opts: { moderationThrow?: unknown; reviewThrow?: unknown } = {}
): { api: ConnectReviewModerationApi; moderationCalls: { reviewerId: string; moderationRequestIds: string[] }[]; reviewCalls: unknown[] } {
	const moderationCalls: { reviewerId: string; moderationRequestIds: string[] }[] = [];
	const reviewCalls: unknown[] = [];

	const reviewClient = createClient(
		ReviewServiceDefinition,
		createRouterTransport((router) =>
			router.service(ReviewServiceDefinition, {
				listUnpublishedReviewsForUser(request) {
					reviewCalls.push(request);
					if (opts.reviewThrow) throw opts.reviewThrow;
					return {
						reviews,
						pagination: create(PageInfoSchema, {
							page: 1n,
							pageSize: 20n,
							totalItems: BigInt(reviews.length),
							totalPages: 1n
						})
					};
				}
			})
		)
	);

	const moderationClient = createClient(
		ReviewModerationServiceDefinition,
		createRouterTransport((router) =>
			router.service(ReviewModerationServiceDefinition, {
				getReviewModerationJobsForReviewer(request) {
					moderationCalls.push({ reviewerId: request.reviewerId, moderationRequestIds: [...request.moderationRequestIds] });
					if (opts.moderationThrow) throw opts.moderationThrow;
					return { jobs };
				}
			})
		)
	);

	return {
		api: new ConnectReviewModerationApi(reviewClient, moderationClient),
		moderationCalls,
		reviewCalls
	};
}

describe('review moderation batch merge API', () => {
 it('returns an empty User page without calling CMS', async () => {
  const { api, moderationCalls } = makeApi([], [], { moderationThrow: new ConnectError('empty batch rejected', Code.InvalidArgument) });
  const page = await api.loadReviewsForUser({ reviewerId: 'rev-1' });
  expect(page.records).toEqual([]);
  expect(page.cmsAvailable).toBe(true);
  expect(moderationCalls).toHaveLength(0);
 });
	it('issues one CMS call per page carrying every request id', async () => {
		const reviews = [
			review('a', { moderationRequestId: 'req-a' }),
			review('b', { moderationRequestId: 'req-b' })
		];
		const { api, moderationCalls } = makeApi(reviews, []);
		const { records } = await api.loadReviewsForUser({ reviewerId: 'rev-1' });
		expect(moderationCalls).toHaveLength(1);
		expect(moderationCalls[0].moderationRequestIds).toEqual(['req-a', 'req-b']);
		expect(records).toHaveLength(2);
	});

	it('passes User page parameters through and returns the page summary', async () => {
		const { api, reviewCalls } = makeApi([review('a')], []);
		const pagination = create(PaginationSchema, { page: 2n, pageSize: 10n });
		const { records, pageInfo } = await api.loadReviewsForUser({
			reviewerId: 'rev-1',
			pagination
		});
		expect(reviewCalls[0]).toMatchObject({
			reviewerId: 'rev-1'
		});
		expect((reviewCalls[0] as { pagination?: { page: bigint } }).pagination?.page).toBe(2n);
		expect(records).toHaveLength(1);
		expect(pageInfo?.totalItems).toBe(1n);
	});

	it('merges CMS grading status into each review without a job', async () => {
		const reviews = [
			review('pending', { moderationStatus: ModerationStatus.PENDING_MODERATION, moderationReasonCodes: [] }),
			review('rejected', {
				moderationStatus: ModerationStatus.REJECTED,
				moderationReasonCodes: ['HARASSMENT']
			})
		];
		const jobs = [
			job('rejected', {
				status: ModerationJobStatus.COMPLETED,
				outcome: ModerationOutcomeType.REJECTED,
				reasonCodes: ['HARASSMENT', 'SPAM'],
				runGeneration: 2
			})
		];
		const { api } = makeApi(reviews, jobs);
		const { records } = await api.loadReviewsForUser({ reviewerId: 'rev-1' });

		const pending = records.find((r) => r.review.id === 'pending')!;
		expect(pending.job).toBeUndefined();
		expect(pending.job).toBeUndefined();

		const rejected = records.find((r) => r.review.id === 'rejected')!;
		expect(rejected.job?.status).toBe(ModerationJobStatus.COMPLETED);
		expect(rejected.job?.outcome).toBe(ModerationOutcomeType.REJECTED);
		expect(rejected.reasonMessages[0]).toBe(
			'It contains harassing content.'
		);
	});

	it('treats a missing job as not-yet-ingested rather than approval', async () => {
		const reviews = [review('orphan', { moderationStatus: ModerationStatus.PENDING_MODERATION })];
		const { api } = makeApi(reviews, []);
		const { records: [record] } = await api.loadReviewsForUser({ reviewerId: 'rev-1' });
		expect(record.job).toBeUndefined();
		expect(record.review.moderationStatus).toBe(ModerationStatus.PENDING_MODERATION);
	});

	it('marks an in-flight job as awaiting moderation', async () => {
		const reviews = [review('mid', { moderationStatus: ModerationStatus.PENDING_MODERATION })];
		const jobs = [job('mid', { status: ModerationJobStatus.RETRY_WAIT })];
		const { api } = makeApi(reviews, jobs);
		const { records: [record] } = await api.loadReviewsForUser({ reviewerId: 'rev-1' });
		expect(record.job?.status).toBe(ModerationJobStatus.RETRY_WAIT);
	});

	it('keeps only the highest generation job per request id', async () => {
		const reviews = [review('dup', { moderationStatus: ModerationStatus.PENDING_MODERATION })];
		const jobs = [
			job('dup', { runGeneration: 1, outcome: ModerationOutcomeType.ERROR }),
			job('dup', { runGeneration: 3, outcome: ModerationOutcomeType.APPROVED })
		];
		const { api } = makeApi(reviews, jobs);
		const { records: [record] } = await api.loadReviewsForUser({ reviewerId: 'rev-1' });
		expect(record.job?.outcome).toBe(ModerationOutcomeType.APPROVED);
		expect(record.job!.runGeneration).toBe(3);
	});

	it('caps the batched request ids at the proto limit', async () => {
		const count = MAX_MODERATION_REQUEST_IDS_PER_BATCH + 10;
		const reviews = Array.from({ length: count }, (_, i) => review(`r${i}`, { moderationRequestId: `req-${i}` }));
		const { api, moderationCalls } = makeApi(reviews, []);
		await api.loadReviewsForUser({ reviewerId: 'rev-1' });
		expect(moderationCalls).toHaveLength(1);
		expect(moderationCalls[0].moderationRequestIds).toHaveLength(MAX_MODERATION_REQUEST_IDS_PER_BATCH);
	});

	it('propagates a CMS authorization ConnectError instead of masking it', async () => {
		const denied = new ConnectError('Denied', Code.PermissionDenied);
		const { api } = makeApi([review('a')], [], { moderationThrow: denied });
		await expect(api.loadReviewsForUser({ reviewerId: 'rev-1' })).rejects.toBeInstanceOf(ConnectError);
	});

	it('degrades a CMS outage to records without grading progress', async () => {
		const down = new ConnectError('connection refused', Code.Unavailable);
		const { api } = makeApi([review('a')], [], { moderationThrow: down });
		const { records, cmsAvailable, cmsError } = await api.loadReviewsForUser({ reviewerId: 'rev-1' });
		expect(records).toHaveLength(1);
		expect(records[0].job).toBeUndefined();
		expect(cmsAvailable).toBe(false);
		expect(cmsError).toBeTruthy();
	});

	it('propagates the User Service ConnectError', async () => {
		const unavailable = new ConnectError('down', Code.Unavailable);
		const { api } = makeApi([review('a')], [], { reviewThrow: unavailable });
		await expect(api.loadReviewsForUser({ reviewerId: 'rev-1' })).rejects.toThrow('down');
	});
});
