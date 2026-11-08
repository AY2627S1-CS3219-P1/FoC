import { createClient, type Transport } from '@connectrpc/connect';
import {
	ReviewService as ReviewServiceDefinition,
	type ModerationStatus
} from '$lib/gen/user/review/v1/review_pb';
import {
	ReviewModerationService as ReviewModerationServiceDefinition
} from '$lib/gen/moderation/review/v1/review_moderation_pb';
import { ConnectReviewModerationApi } from './connectReviewModerationApi.ts';
import { ReviewModerationService } from './service.svelte.ts';

// Wraps two services on two transports: User Service's unpublished-review
// listing and the CMS's owner-job listing. The CMS call batches one page of
// moderation_request_ids, so the factory wires both transports and exposes one
// stateful service.
export function createReviewModerationService(
	userTransport: Transport,
	moderationTransport: Transport
): ReviewModerationService {
	const review = createClient(ReviewServiceDefinition, userTransport);
	const moderation = createClient(ReviewModerationServiceDefinition, moderationTransport);
	return new ReviewModerationService(new ConnectReviewModerationApi(review, moderation));
}

export type { ReviewModerationService };
export type { ModerationStatus };
export type {
	ReviewModerationApi,
	ReviewModerationRecord,
	LoadReviewsForUserParams
} from './reviewModerationApi.ts';
