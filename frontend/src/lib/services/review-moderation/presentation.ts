import type { ReviewModerationRecord } from './reviewModerationApi';
import { ModerationJobStatus, ModerationOutcomeType } from '$lib/gen/moderation/review/v1/review_moderation_pb';
import { ModerationStatus } from '$lib/gen/user/review/v1/review_pb';
export interface Presentation {
	label: string;
	tone: 'pending' | 'rejected' | 'error' | 'approved';
	detail: string;
}

export function presentReviewModeration(record: ReviewModerationRecord): Presentation {
	const { review, job } = record;
	if (job?.configurationPaused && (review.moderationStatus === ModerationStatus.PENDING_MODERATION ||
		job.runGeneration > review.appliedRunGeneration)) {
		return { label: 'Assessment paused', tone: 'pending', detail: 'An administrator must correct the assessment configuration. Your review remains unpublished.' };
	}
	if (review.moderationStatus === ModerationStatus.REJECTED) {
		return {
			label: 'Rejected',
			tone: 'rejected',
			detail: record.reasonMessages.join(' ')
		};
	}
	if (review.moderationStatus === ModerationStatus.MODERATION_ERROR && job &&
		job.runGeneration > review.appliedRunGeneration) {
		switch (job.status) {
			case ModerationJobStatus.QUEUED:
				return { label: 'Assessment retry queued', tone: 'pending', detail: 'An administrator restarted assessment. Your review remains unpublished.' };
			case ModerationJobStatus.PROCESSING:
				return { label: 'Being assessed again', tone: 'pending', detail: 'The new assessment is in progress. Your review remains unpublished.' };
			case ModerationJobStatus.RETRY_WAIT:
				return { label: 'Assessment retry scheduled', tone: 'pending', detail: 'The new assessment is waiting to retry. Your review remains unpublished.' };
			case ModerationJobStatus.COMPLETED:
				return { label: 'Assessment completed · awaiting publication decision', tone: 'pending', detail: 'User Service has not yet applied the new result. Your review remains unpublished.' };
		}
	}
	if (review.moderationStatus === ModerationStatus.MODERATION_ERROR) {
		return {
			label: 'Assessment error',
			tone: 'error',
			detail:
				'We could not complete an assessment for this review. An administrator can retry it; the review is not published.'
		};
	}
	// Pending on User Service; the CMS job explains what is happening.
	if (!job) {
		return {
			label: 'Awaiting assessment',
			tone: 'pending',
			detail: 'Your review was submitted and is waiting to be assessed.'
		};
	}
	switch (job.status) {
		case ModerationJobStatus.QUEUED:
			return { label: 'Assessment queued', tone: 'pending', detail: 'Your review is in the assessment queue.' };
		case ModerationJobStatus.PROCESSING:
			return { label: 'Being assessed', tone: 'pending', detail: 'Your review is being assessed right now.' };
		case ModerationJobStatus.RETRY_WAIT:
			return {
				label: 'Assessment retry scheduled',
				tone: 'pending',
				detail: 'An assessment attempt did not finish; the next one is scheduled.'
			};
		case ModerationJobStatus.COMPLETED:
			if (job.outcome === ModerationOutcomeType.APPROVED) {
				return {
					label: 'Approved · publishing shortly',
					tone: 'approved',
					detail: 'The assessment approved your review; publication follows automatically.'
				};
			}
			if (job.outcome === ModerationOutcomeType.REJECTED) {
				return { label: 'Rejected', tone: 'rejected', detail: record.reasonMessages.join(' ') };
			}
			return {
				label: 'Assessment error',
				tone: 'error',
				detail: 'The assessment ended without a verdict. An administrator can retry it.'
			};
		case ModerationJobStatus.ERRORED:
			return {
				label: 'Assessment error',
				tone: 'error',
				detail: 'The assessment ended without a verdict. An administrator can retry it.'
			};
		default:
			return { label: 'Awaiting assessment', tone: 'pending', detail: 'Your review is waiting to be assessed.' };
	}
}
