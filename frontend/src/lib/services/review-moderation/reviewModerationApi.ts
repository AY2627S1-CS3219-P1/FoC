import type {
	Pagination,
	PageInfo
} from '$lib/gen/pagination/v1/pagination_pb';
import {
	ModerationJobStatus,
	type ReviewModerationJobSummary
} from '$lib/gen/moderation/review/v1/review_moderation_pb';
import type { UnpublishedReview } from '$lib/gen/user/review/v1/review_pb';
import { moderationReasonMessage } from './reasonMessages.ts';

export const MAX_MODERATION_REQUEST_IDS_PER_BATCH = 100;

/** User publication state is authoritative; a missing CMS job never implies approval. */
export interface ReviewModerationRecord {
	review: UnpublishedReview;
	job?: ReviewModerationJobSummary;
	reasonMessages: string[];
}

export interface LoadReviewsForUserParams {
	reviewerId: string;
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
	const hasVerdict = !!job && job.status === ModerationJobStatus.COMPLETED;
	const reasonCodes = hasVerdict ? job!.reasonCodes : review.moderationReasonCodes;

	return {
		review,
		job,
		reasonMessages: reasonCodes.map((code) => moderationReasonMessage(code))
	};
}

export function batchedModerationRequestIds(reviews: readonly UnpublishedReview[]): string[] {
	return reviews
		.map((review) => review.moderationRequestId)
		.filter((id) => id.length > 0)
		.slice(0, MAX_MODERATION_REQUEST_IDS_PER_BATCH);
}
