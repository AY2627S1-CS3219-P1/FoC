import { describe, it, expect } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { UnpublishedReviewSchema, ModerationStatus } from '$lib/gen/user/review/v1/review_pb';
import { ReviewModerationJobSummarySchema, ModerationJobStatus, ModerationOutcomeType } from '$lib/gen/moderation/review/v1/review_moderation_pb';
import { mergeReviewModeration } from './reviewModerationApi';
import { presentReviewModeration } from './presentation';

describe('newer retry progress over the last applied error', () => {
	it.each([
		[ModerationJobStatus.QUEUED, 'Assessment retry queued'],
		[ModerationJobStatus.PROCESSING, 'Being assessed again'],
		[ModerationJobStatus.RETRY_WAIT, 'Assessment retry scheduled'],
		[ModerationJobStatus.COMPLETED, 'Assessment completed · awaiting publication decision']
	])('shows newer generation status %s without publishing', (status, label) => {
		const review = create(UnpublishedReviewSchema, { moderationStatus: ModerationStatus.MODERATION_ERROR, appliedRunGeneration: 1 });
		const job = create(ReviewModerationJobSummarySchema, { status, runGeneration: 2, outcome: ModerationOutcomeType.APPROVED });
		const record = mergeReviewModeration(review, job);
		expect(presentReviewModeration(record).label).toBe(label);
		expect(record.publicationStatus).toBe(ModerationStatus.MODERATION_ERROR);
		expect(presentReviewModeration(record).detail).toContain('unpublished');
	});
	it('retains the error for stale progress', () => {
		const review = create(UnpublishedReviewSchema, { moderationStatus: ModerationStatus.MODERATION_ERROR, appliedRunGeneration: 2 });
		const job = create(ReviewModerationJobSummarySchema, { status: ModerationJobStatus.PROCESSING, runGeneration: 1 });
		expect(presentReviewModeration(mergeReviewModeration(review, job)).label).toBe('Assessment error');
	});
});

