import { createClient, type Transport } from '@connectrpc/connect';
import {
	ReviewService as ReviewServiceDefinition
} from '$lib/gen/user/review/v1/review_pb';
import {
	ReviewModerationService as ReviewModerationServiceDefinition
} from '$lib/gen/moderation/review/v1/review_moderation_pb';
import { ConnectReviewModerationApi } from './connectReviewModerationApi.ts';
import { ReviewModerationService } from './service.ts';

export function createReviewModerationService(
	userTransport: Transport,
	moderationTransport: Transport
): ReviewModerationService {
	const review = createClient(ReviewServiceDefinition, userTransport);
	const moderation = createClient(ReviewModerationServiceDefinition, moderationTransport);
	return new ReviewModerationService(new ConnectReviewModerationApi(review, moderation));
}

export type { ReviewModerationService };
export type {
	ReviewModerationApi,
	ReviewModerationRecord,
	LoadReviewsForUserParams
} from './reviewModerationApi.ts';

export { ReviewPageState } from './pageState.svelte.ts';
