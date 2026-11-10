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
				return { label: 'Assessment retry queued', tone: 'pending', detail: 'An administrator restarted the assessment.' };
			case ModerationJobStatus.PROCESSING:
				return { label: 'Being assessed again', tone: 'pending', detail: 'The new assessment is running.' };
			case ModerationJobStatus.RETRY_WAIT:
				return { label: 'Assessment retry scheduled', tone: 'pending', detail: 'The new assessment will start shortly.' };
			case ModerationJobStatus.COMPLETED:
				return { label: 'Assessment completed · awaiting publication decision', tone: 'pending', detail: 'The new result hasn\u2019t been applied yet.' };
		}
	}
	if (review.moderationStatus === ModerationStatus.MODERATION_ERROR) {
		return {
			label: 'Assessment error',
			tone: 'error',
			detail: 'The assessment didn\u2019t finish. An administrator can retry it.'
		};
	}
	if (!job) {
		return {
			label: 'Awaiting assessment',
			tone: 'pending',
			detail: 'It\u2019s published automatically once approved.'
		};
	}
	switch (job.status) {
		case ModerationJobStatus.QUEUED:
			return { label: 'Assessment queued', tone: 'pending', detail: 'It\u2019s published automatically once approved.' };
		case ModerationJobStatus.PROCESSING:
			return { label: 'Being assessed', tone: 'pending', detail: 'It\u2019s published automatically once approved.' };
		case ModerationJobStatus.RETRY_WAIT:
			return {
				label: 'Assessment retry scheduled',
				tone: 'pending',
				detail: 'The next attempt is scheduled.'
			};
		case ModerationJobStatus.COMPLETED:
			if (job.outcome === ModerationOutcomeType.APPROVED) {
				return {
					label: 'Approved · publishing shortly',
					tone: 'approved',
					detail: 'No action needed.'
				};
			}
			if (job.outcome === ModerationOutcomeType.REJECTED) {
				return { label: 'Rejected', tone: 'rejected', detail: record.reasonMessages.join(' ') };
			}
			return {
				label: 'Assessment error',
				tone: 'error',
				detail: 'The assessment didn\u2019t finish. An administrator can retry it.'
			};
		case ModerationJobStatus.ERRORED:
			return {
				label: 'Assessment error',
				tone: 'error',
				detail: 'The assessment didn\u2019t finish. An administrator can retry it.'
			};
		default:
			return { label: 'Awaiting assessment', tone: 'pending', detail: 'It\u2019s published automatically once approved.' };
	}
}
