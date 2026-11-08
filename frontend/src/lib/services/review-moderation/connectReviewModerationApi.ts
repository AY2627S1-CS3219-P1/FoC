import type { Client } from '@connectrpc/connect';
import type { Pagination } from '$lib/gen/pagination/v1/pagination_pb';
import {
	ModerationStatus,
	ReviewService as ReviewServiceDefinition,
	type UnpublishedReview
} from '$lib/gen/user/review/v1/review_pb';
import {
	ReviewModerationService as ReviewModerationServiceDefinition,
	type ReviewModerationJobSummary
} from '$lib/gen/moderation/review/v1/review_moderation_pb';
import type { LoadReviewsForUserParams, ReviewModerationApi, ReviewModerationRecord } from './reviewModerationApi.ts';
import { batchedModerationRequestIds, mergeReviewModeration } from './reviewModerationApi.ts';

type ReviewClient = Client<typeof ReviewServiceDefinition>;
type ModerationClient = Client<typeof ReviewModerationServiceDefinition>;

// The raw Connect adapter over User Service's review listing and the CMS
// owner-job listing. It performs one batched CMS call per page and merges the
// results per review; it does not swallow transport errors.
export class ConnectReviewModerationApi implements ReviewModerationApi {
	readonly #review: ReviewClient;
	readonly #moderation: ModerationClient;

	constructor(review: ReviewClient, moderation: ModerationClient) {
		this.#review = review;
		this.#moderation = moderation;
	}

	async loadReviewsForUser({
		reviewerId,
		moderationStatusFilter,
		pagination
	}: LoadReviewsForUserParams): Promise<ReviewModerationRecord[]> {
		const { reviews } = await this.#review.listUnpublishedReviewsForUser({
			reviewerId,
			moderationStatusFilter,
			pagination
		});
		const { jobs } = await this.#moderation.getReviewModerationJobsForReviewer({
			reviewerId,
			moderationRequestIds: batchedModerationRequestIds(reviews)
		});
		return reviews.map((review) => mergeReviewModeration(review, jobById(jobs, review.moderationRequestId)));
	}
}

// A request may have more than one job summary in edge cases (for example a
// stale completion racing a fresh attempt); keep the highest generation so the
// merge reflects the latest grading.
function jobById(
	jobs: readonly ReviewModerationJobSummary[],
	moderationRequestId: string
): ReviewModerationJobSummary | undefined {
	let best: ReviewModerationJobSummary | undefined;
	for (const job of jobs) {
		if (job.moderationRequestId !== moderationRequestId) continue;
		if (!best || job.runGeneration > best.runGeneration) best = job;
	}
	return best;
}

// Re-exported so callers can filter a page's reviews by publication state
// without importing the generated enum alongside the service.
export { ModerationStatus };
