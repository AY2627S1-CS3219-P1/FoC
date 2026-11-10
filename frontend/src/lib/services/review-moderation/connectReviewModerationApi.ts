import { Code, ConnectError } from '@connectrpc/connect';
import type { Client } from '@connectrpc/connect';
import {
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

// Degrade CMS availability failures; propagate auth and validation errors to interceptors.
const DEGRADABLE_CMS_CODES: readonly Code[] = [
	Code.Unavailable,
	Code.DeadlineExceeded,
	Code.Internal,
	Code.Unknown,
	Code.NotFound,
	Code.DataLoss
];

export class ConnectReviewModerationApi implements ReviewModerationApi {
	readonly #review: ReviewClient;
	readonly #moderation: ModerationClient;

	constructor(review: ReviewClient, moderation: ModerationClient) {
		this.#review = review;
		this.#moderation = moderation;
	}

	async loadReviewsForUser({
		reviewerId,
		pagination
	}: LoadReviewsForUserParams): Promise<ReviewModerationPage> {
		const { reviews, pagination: pageInfo } = await this.#review.listUnpublishedReviewsForUser({
			reviewerId,
			pagination
		});

		// CMS outages must not erase User's canonical collection.
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
				cmsError = error.rawMessage || 'Moderation progress is unavailable right now.';
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

// Prefer the latest generation if duplicate summaries arrive.
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
