import { describe, it, expect } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { UnpublishedReviewSchema, ModerationStatus } from '$lib/gen/user/review/v1/review_pb';
import { ReviewModerationJobSummarySchema, ModerationJobStatus, ModerationOutcomeType } from '$lib/gen/moderation/review/v1/review_moderation_pb';
import { mergeReviewModeration } from './reviewModerationApi';
import { presentReviewModeration } from './presentation';

describe('newer retry progress over the last applied error', () => {
	it.each([
		[ModerationJobStatus.QUEUED, 'Assessment retry queued', 'An administrator restarted the assessment.'],
		[ModerationJobStatus.PROCESSING, 'Being assessed again', 'The new assessment is running.'],
		[ModerationJobStatus.RETRY_WAIT, 'Assessment retry scheduled', 'The new assessment will start shortly.'],
		[ModerationJobStatus.COMPLETED, 'Assessment completed · awaiting publication decision', 'The new result hasn\u2019t been applied yet.']
	])('shows newer generation status %s without publishing', (status, label, detail) => {
		const review = create(UnpublishedReviewSchema, { moderationStatus: ModerationStatus.MODERATION_ERROR, appliedRunGeneration: 1 });
		const job = create(ReviewModerationJobSummarySchema, { status, runGeneration: 2, outcome: ModerationOutcomeType.APPROVED });
		const record = mergeReviewModeration(review, job);
		expect(presentReviewModeration(record).label).toBe(label);
		expect(presentReviewModeration(record).detail).toBe(detail);
		expect(record.review.moderationStatus).toBe(ModerationStatus.MODERATION_ERROR);
	});
	it('retains the error for stale progress', () => {
		const review = create(UnpublishedReviewSchema, { moderationStatus: ModerationStatus.MODERATION_ERROR, appliedRunGeneration: 2 });
		const job = create(ReviewModerationJobSummarySchema, { status: ModerationJobStatus.PROCESSING, runGeneration: 1 });
		expect(presentReviewModeration(mergeReviewModeration(review, job)).label).toBe('Assessment error');
	});
});
