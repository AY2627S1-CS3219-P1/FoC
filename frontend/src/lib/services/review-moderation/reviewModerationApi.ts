import type {
	Pagination,
	PageInfo
} from '$lib/gen/pagination/v1/pagination_pb';
import {
	ModerationJobStatus,
	ModerationOutcomeType,
	type ReviewModerationJobSummary
} from '$lib/gen/moderation/review/v1/review_moderation_pb';
import type { UnpublishedReview } from '$lib/gen/user/review/v1/review_pb';
import { ModerationStatus } from '$lib/gen/user/review/v1/review_pb';
import { moderationReasonMessage } from './reasonMessages.ts';

// GetReviewModerationJobsForReviewer carries one User page of request IDs in a
// single request; larger sets must be paginated, never sent at once.
export const MAX_MODERATION_REQUEST_IDS_PER_BATCH = 100;

// CMS job statuses that still have grading work outstanding.
const NON_TERMINAL_JOB_STATUSES: readonly ModerationJobStatus[] = [
	ModerationJobStatus.QUEUED,
	ModerationJobStatus.PROCESSING,
	ModerationJobStatus.RETRY_WAIT
];

/**
 * One unpublished review merged with the CMS job progress for its current
 * moderation request. Publication state is authoritative on User Service; the
 * CMS job explains grading progress behind a still-pending publication. A
 * missing job means the request has not been ingested yet, never approval.
 */
export interface ReviewModerationRecord {
	/** The User Service review as returned on the page. */
	review: UnpublishedReview;
	/** CMS job for this review's current request, when it has been ingested. */
	job?: ReviewModerationJobSummary;
	/** Authoritative publication state on User Service. */
	publicationStatus: ModerationStatus;
	/** CMS grading status, or UNSPECIFIED when no job is present. */
	jobStatus: ModerationJobStatus;
	/** CMS verdict once the job completes, or UNSPECIFIED while in flight. */
	outcome: ModerationOutcomeType;
	/**
	 * Reason codes for the review's current state: the completed CMS verdict,
	 * otherwise the codes User Service has applied. Empty for approvals and for
	 * reviews with no verdict yet.
	 */
	reasonCodes: string[];
	/** User-facing explanations for `reasonCodes`, in order. */
	reasonMessages: string[];
	/** Whether the CMS still has grading work outstanding for this review. */
	isAwaitingModeration: boolean;
}

export interface LoadReviewsForUserParams {
	/** Reviewer whose unpublished reviews are listed. */
	reviewerId: string;
	/** Optional publication-state filter. */
	moderationStatusFilter?: ModerationStatus;
	/** Optional User Service page cursor. */
	pagination?: Pagination;
}

/** One loaded page: merged records plus User Service's page summary. */
export interface ReviewModerationPage {
	records: ReviewModerationRecord[];
	/** Response-side page summary; undefined when the backend omitted it. */
	pageInfo?: PageInfo;
	/**
	 * False when the CMS job lookup degraded (outage) and records carry no
	 * grading progress this load. User's list is authoritative either way.
	 */
	cmsAvailable: boolean;
	/** User-facing degradation message when cmsAvailable is false. */
	cmsError?: string;
}

// The consumer-owned API: it lists a reviewer's unpublished reviews and
// enriches each with the CMS grading progress for its current request, in a
// single batched CMS call. Connect transport errors (for example requesting
// another reviewer without authorization) propagate so the transport-layer auth
// interceptor can handle them.
export interface ReviewModerationApi {
	loadReviewsForUser(params: LoadReviewsForUserParams): Promise<ReviewModerationPage>;
}

// Merge a review with its CMS job: derive the status/outcome, the effective
// reason codes (completed verdict preferred over the applied codes), the
// mirrored messages, and whether grading is still outstanding.
export function mergeReviewModeration(
	review: UnpublishedReview,
	job: ReviewModerationJobSummary | undefined
): ReviewModerationRecord {
	const jobStatus = job?.status ?? ModerationJobStatus.UNSPECIFIED;
	const outcome = job?.outcome ?? ModerationOutcomeType.UNSPECIFIED;
	const hasVerdict = !!job && job.status === ModerationJobStatus.COMPLETED;
	const reasonCodes = hasVerdict ? job!.reasonCodes : review.moderationReasonCodes;
	const awaiting = job !== undefined
		? NON_TERMINAL_JOB_STATUSES.includes(jobStatus)
		: review.moderationStatus === ModerationStatus.PENDING_MODERATION;

	return {
		review,
		job,
		publicationStatus: review.moderationStatus,
		jobStatus,
		outcome,
		reasonCodes,
		reasonMessages: reasonCodes.map((code) => moderationReasonMessage(code)),
		isAwaitingModeration: awaiting
	};
}

// The single CMS call is limited to MAX_MODERATION_REQUEST_IDS_PER_BATCH IDs.
export function batchedModerationRequestIds(reviews: readonly UnpublishedReview[]): string[] {
	return reviews
		.map((review) => review.moderationRequestId)
		.filter((id) => id.length > 0)
		.slice(0, MAX_MODERATION_REQUEST_IDS_PER_BATCH);
}
