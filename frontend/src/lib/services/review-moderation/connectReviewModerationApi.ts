import { Code, ConnectError, createClient, createRouterTransport } from '@connectrpc/connect';
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
import type {
	LoadReviewsForUserParams,
	ReviewModerationApi,
	ReviewModerationPage,
	ReviewModerationRecord
} from './reviewModerationApi.ts';
import { batchedModerationRequestIds, mergeReviewModeration } from './reviewModerationApi.ts';

type ReviewClient = Client<typeof ReviewServiceDefinition>;
type ModerationClient = Client<typeof ReviewModerationServiceDefinition>;

// CMS failures that degrade to "no job progress shown" instead of failing the
// whole page. Unauthenticated, permission-denied and invalid-argument errors
// still propagate so the transport-layer interceptors and the caller see them.
const DEGRADABLE_CMS_CODES: readonly Code[] = [
	Code.Unavailable,
	Code.DeadlineExceeded,
	Code.Internal,
	Code.Unknown,
	Code.NotFound,
	Code.DataLoss
];

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
	}: LoadReviewsForUserParams): Promise<ReviewModerationPage> {
		const { reviews, pagination: pageInfo } = await this.#review.listUnpublishedReviewsForUser({
			reviewerId,
			moderationStatusFilter,
			pagination
		});

		// A CMS outage must not erase User's canonical collection: transport
		// failures degrade to records without job progress, while authorization
		// and request-shape errors still propagate to the transport layer.
		const requestIds = batchedModerationRequestIds(reviews);
		let jobs: ReviewModerationJobSummary[] = [];
		let cmsAvailable = true;
		let cmsError: string | undefined;
		try {
			if (requestIds.length > 0) jobs = (
				await this.#moderation.getReviewModerationJobsForReviewer({
					reviewerId,
					moderationRequestIds: requestIds
				})
			).jobs;
		} catch (error) {
			if (error instanceof ConnectError && DEGRADABLE_CMS_CODES.includes(error.code)) {
				cmsAvailable = false;
				cmsError = error.rawMessage || 'Moderation progress is temporarily unavailable.';
			} else {
				throw error;
			}
		}
		return {
			records: reviews.map((review) =>
				mergeReviewModeration(review, jobById(jobs, review.moderationRequestId))
			),
			pageInfo,
			cmsAvailable,
			cmsError
		};
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
