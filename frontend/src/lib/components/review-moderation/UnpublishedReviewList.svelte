<script lang="ts">
	import { Button, Card } from 'm3-svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Timestamp } from '@bufbuild/protobuf/wkt';
	import type { ReviewModerationRecord } from '$lib/services/review-moderation/reviewModerationApi';
	import { ModerationJobStatus, ModerationOutcomeType } from '$lib/gen/moderation/review/v1/review_moderation_pb';
	import { ModerationStatus } from '$lib/gen/user/review/v1/review_pb';

	// Shared presentation for one page of unpublished reviews. Pure display:
	// both the owner page and the admin reviewer-inspection page render it, and
	// neither may assume the other's authorization.
	let { records }: { records: readonly ReviewModerationRecord[] } = $props();

	interface Presentation {
		label: string;
		tone: 'pending' | 'rejected' | 'error' | 'approved';
		detail: string;
	}

	function present(record: ReviewModerationRecord): Presentation {
		const { review, job } = record;
		if (review.moderationStatus === ModerationStatus.REJECTED) {
			return {
				label: 'Rejected',
				tone: 'rejected',
				detail: record.reasonMessages.join(' ')
			};
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

	function formatDate(value: Timestamp | undefined): string {
		if (!value) return '—';
		return timestampDate(value).toLocaleString();
	}
</script>

<ul class="unpublished-list" aria-label="Unpublished reviews">
	{#each records as record (record.review.id)}
		{@const view = present(record)}
		<li>
			<Card variant="outlined">
				<div class="review-body" data-tone={view.tone}>
					<header class="review-heading">
						<span class="status-chip" data-tone={view.tone}>{view.label}</span>
						<span class="review-meta">
							{record.review.rating}/5 · {formatDate(record.review.createdAt)}
						</span>
					</header>
					<p class="review-comment">{record.review.comment}</p>
					{#if view.detail}<p class="review-detail" role="status">{view.detail}</p>{/if}
					{#if record.review.orderId}
						<p class="review-meta">Order {record.review.orderId}</p>
					{/if}
				</div>
			</Card>
		</li>
	{/each}
</ul>

<style>
	.unpublished-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 1rem;
	}
	.review-body {
		display: grid;
		gap: 0.5rem;
	}
	.review-heading {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}
	.review-meta {
		color: var(--md-sys-color-on-surface-variant, #5f6368);
		font-size: 0.875rem;
	}
	.review-comment {
		margin: 0;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.review-detail {
		margin: 0;
		font-size: 0.9rem;
		color: var(--md-sys-color-on-surface-variant, #5f6368);
	}
	.status-chip {
		display: inline-block;
		padding: 0.2rem 0.75rem;
		border-radius: 999px;
		font-size: 0.8rem;
		font-weight: 600;
	}
	.status-chip[data-tone='pending'] {
		background: var(--md-sys-color-secondary-container, #e8def8);
		color: var(--md-sys-color-on-secondary-container, #1d192b);
	}
	.status-chip[data-tone='approved'] {
		background: var(--md-sys-color-primary-container, #d7e3ff);
		color: var(--md-sys-color-on-primary-container, #001b3f);
	}
	.status-chip[data-tone='rejected'] {
		background: var(--md-sys-color-error-container, #f9dedc);
		color: var(--md-sys-color-on-error-container, #410e0b);
	}
	.status-chip[data-tone='error'] {
		background: var(--md-sys-color-tertiary-container, #ffd8e4);
		color: var(--md-sys-color-on-tertiary-container, #31111d);
	}
</style>
