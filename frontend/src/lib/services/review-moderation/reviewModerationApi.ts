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

export const MAX_MODERATION_REQUEST_IDS_PER_BATCH = 100;

const NON_TERMINAL_JOB_STATUSES: readonly ModerationJobStatus[] = [
	ModerationJobStatus.QUEUED,
	ModerationJobStatus.PROCESSING,
	ModerationJobStatus.RETRY_WAIT
];

/** User publication state is authoritative; a missing CMS job never implies approval. */
export interface ReviewModerationRecord {
	review: UnpublishedReview;
	job?: ReviewModerationJobSummary;
	publicationStatus: ModerationStatus;
	jobStatus: ModerationJobStatus;
	outcome: ModerationOutcomeType;
	/** Prefer completed CMS verdict codes; otherwise use User's applied codes. */
	reasonCodes: string[];
	reasonMessages: string[];
	isAwaitingModeration: boolean;
}

export interface LoadReviewsForUserParams {
	reviewerId: string;
	moderationStatusFilter?: ModerationStatus;
	pagination?: Pagination;
}

export interface ReviewModerationPage {
	records: ReviewModerationRecord[];
	pageInfo?: PageInfo;
	/** False when CMS was unavailable; User's review list remains authoritative. */
	cmsAvailable: boolean;
	cmsError?: string;
}

export interface ReviewModerationApi {
	loadReviewsForUser(params: LoadReviewsForUserParams): Promise<ReviewModerationPage>;
}

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

export function batchedModerationRequestIds(reviews: readonly UnpublishedReview[]): string[] {
	return reviews
		.map((review) => review.moderationRequestId)
		.filter((id) => id.length > 0)
		.slice(0, MAX_MODERATION_REQUEST_IDS_PER_BATCH);
}
