import { describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { UnpublishedReviewSchema } from '$lib/gen/user/review/v1/review_pb';
import { ReviewModerationService } from './service.ts';
import { ReviewPageState } from './pageState.svelte.ts';
import { mergeReviewModeration, type ReviewModerationPage } from './reviewModerationApi.ts';

function page(comment: string): ReviewModerationPage {
	return {
		records: [mergeReviewModeration(create(UnpublishedReviewSchema, { comment }), undefined)],
		cmsAvailable: true
	};
}

function deferred() {
	let resolve!: (page: ReviewModerationPage) => void;
	let reject!: (error: Error) => void;
	const promise = new Promise<ReviewModerationPage>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}

describe('page-local review data', () => {
	it('never shares records between page instances using the same service', async () => {
		const load = vi.fn(async () => page('private comment'));
		const service = new ReviewModerationService({ loadReviewsForUser: load });
		const first = new ReviewPageState(service, () => 'admin');
		await first.loadReviews({ reviewerId: 'reviewer-a' });
		const second = new ReviewPageState(service, () => 'admin');
		expect(second.records).toEqual([]);
		expect(second.hasLoaded).toBe(false);
		await second.loadReviews({ reviewerId: 'reviewer-a' });
		expect(load).toHaveBeenCalledTimes(2);
	});

	it('clears another reviewer immediately and ignores their late response', async () => {
		const old = deferred();
		const load = vi
			.fn()
			.mockResolvedValueOnce(page('first reviewer'))
			.mockReturnValueOnce(old.promise)
			.mockResolvedValueOnce(page('second reviewer'));
		const state = new ReviewPageState(
			new ReviewModerationService({ loadReviewsForUser: load }),
			() => 'admin'
		);
		await state.loadReviews({ reviewerId: 'a' });
		const pending = state.loadReviews({ reviewerId: 'a' });
		const next = state.loadReviews({ reviewerId: 'b' });
		expect(state.records).toEqual([]);
		await next;
		old.resolve(page('private old response'));
		expect(await pending).toBe(false);
		expect(state.records[0].review.comment).toBe('second reviewer');
	});

	it('hides records on account changes and ignores a pending result without relying on clear', async () => {
		let account: string | undefined = 'account-a';
		const pending = deferred();
		const load = vi
			.fn()
			.mockResolvedValueOnce(page('account-a private data'))
			.mockReturnValueOnce(pending.promise);
		const state = new ReviewPageState(
			new ReviewModerationService({ loadReviewsForUser: load }),
			() => account
		);
		await state.loadReviews({ reviewerId: 'a' });
		const request = state.loadReviews({ reviewerId: 'a' });
		account = 'account-b';
		expect(state.records).toEqual([]);
		pending.resolve(page('late account-a data'));
		expect(await request).toBe(false);
		expect(state.records).toEqual([]);
	});

	it('ignores an old error after a new request and after unmount cleanup', async () => {
		const old = deferred();
		const load = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(page('latest'));
		const state = new ReviewPageState(
			new ReviewModerationService({ loadReviewsForUser: load }),
			() => 'account'
		);
		const request = state.loadReviews({ reviewerId: 'a' });
		await state.loadReviews({ reviewerId: 'a' });
		old.reject(new Error('old failure'));
		expect(await request).toBe(false);
		expect(state.loadError).toBeNull();
		expect(state.isLoading).toBe(false);
		const late = deferred();
		load.mockReturnValueOnce(late.promise);
		const leaving = state.loadReviews({ reviewerId: 'a' });
		state.clear();
		late.resolve(page('after unmount'));
		expect(await leaving).toBe(false);
		expect(state.records).toEqual([]);
	});
});
