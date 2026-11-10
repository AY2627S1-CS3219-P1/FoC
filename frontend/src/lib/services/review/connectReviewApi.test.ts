import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { Code, ConnectError } from '@connectrpc/connect';
import { ReviewService as ReviewServiceDefinition } from '$lib/gen/user/review/v1/review_pb';
import { ReviewAlreadyExistsError, ReviewRemovedError, ReviewLimitedError, ReviewNotReadyError, ReviewRejectedError } from './reviewApi.ts';
import { ErrorDetailSchema } from '$lib/gen/common/v1/error_pb';
import { ConnectReviewApi } from './connectReviewApi.ts';

function errorWithDetail(reason: string, message: string, photoIds: string[] = []): ConnectError {
	const detail = {
		reason,
		message,
		fieldViolations: photoIds.map((id) => ({ field: 'photo_ids', description: `${id}: ${message}` }))
	};
	return new ConnectError(message, Code.Internal, undefined, [
		{ desc: ErrorDetailSchema, value: detail }
	]);
}

function scenario() {
	const client = {
		getReviewPhotoDownload: vi.fn(async () => ({ photo: {
			id: 'photo-1', url: 'https://storage.invalid/fresh', contentType: 'image/png',
			expiresAt: { seconds: 1700003600n, nanos: 0 }
		} })),
		createReviewPhotoUpload: vi.fn(async () => ({
			photoId: 'photo-1', uploadUrl: 'https://storage.invalid/upload', uploadHeaders: { 'x-test': '1' }, expiresAt: undefined
		})),
		submitReview: vi.fn(async () => ({})),
		listMyReviewsForOrders: vi.fn(async () => ({ reviewedOrderIds: [], removedOrderIds: [] })),
		getMyReviewForOrder: vi.fn(async () => ({ review: undefined })),
		listMyReviews: vi.fn(async () => ({
			reviews: [{
				id: 'review-3', reviewerId: 'me', reviewerName: 'Me', revieweeId: 'them', revieweeName: 'Them',
				orderId: 'order-3', rating: 4, comment: 'Written', createdAt: { seconds: 1700000000n, nanos: 0 }, photos: []
			}],
			pagination: { page: 1, pageSize: 10, totalItems: 1n, totalPages: 1n }
		})),
		listMyReviewsReceived: vi.fn(async () => ({
			reviews: [{
				id: 'review-2', reviewerId: 'r', reviewerName: 'Reviewer', revieweeId: 'e', revieweeName: 'Reviewee',
				orderId: 'order-2', rating: 5, comment: 'Received', createdAt: { seconds: 1700000000n, nanos: 0 }, photos: []
			}],
			pagination: { page: 2, pageSize: 10, totalItems: 11n, totalPages: 2n }
		})),
		listReviewsForUser: vi.fn(async () => ({
			reviews: [{
				id: 'review-1', reviewerId: 'r', reviewerName: 'Reviewer', revieweeId: 'e', revieweeName: 'Reviewee',
				orderId: 'order-1', rating: 4, comment: 'Great', createdAt: { seconds: 1700000000n, nanos: 0 },
				photos: [{ id: 'photo-1', url: 'https://storage.invalid/get', contentType: 'image/png', expiresAt: { seconds: 1700003600n, nanos: 0 } }]
			}],
			pagination: { page: 1, pageSize: 20, totalItems: 1n, totalPages: 1n }
		}))
	} as unknown as Client<typeof ReviewServiceDefinition>;
	const api = new ConnectReviewApi(client);
	return { client, api };
}

describe('ConnectReviewApi', () => {
	it('requests one grant per photo and forwards the storage headers', async () => {
		const { client, api } = scenario();
		const grant = await api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1234 }, new AbortController().signal);
		expect(grant).toEqual({ photoId: 'photo-1', uploadUrl: 'https://storage.invalid/upload', uploadHeaders: { 'x-test': '1' } });
		expect(client.createReviewPhotoUpload).toHaveBeenCalledWith({ contentType: 'image/png', sizeBytes: 1234n }, expect.anything());
	});

	it('submits the review with the caller, submission and ordered photo ids', async () => {
		const { client, api } = scenario();
		const input = { from: 'r', to: 'e', orderId: 'order-1', rating: 5, comment: ' Perfect ', photoIds: ['p2', 'p1'] };
		await api.submit(input, new AbortController().signal);
		expect(client.submitReview).toHaveBeenCalledWith({
			from: 'r', to: 'e', orderId: 'order-1',
			submission: { rating: 5, comment: ' Perfect ' },
			photoIds: ['p2', 'p1']
		}, expect.anything());
	});

	it('maps list responses with photo records and epoch dates', async () => {
		const { client, api } = scenario();
		const page = await api.listForUser('e', { page: 1, pageSize: 3 }, new AbortController().signal);
		expect(page.reviews).toHaveLength(1);
		expect(page.totalPages).toBe(1);
		expect(page.totalItems).toBe(1);
		expect(page.reviews[0]).toMatchObject({
			id: 'review-1', rating: 4, comment: 'Great',
			createdAt: 1700000000000,
			photos: [{ id: 'photo-1', url: 'https://storage.invalid/get', contentType: 'image/png', expiresAt: 1700003600000 }]
		});
		expect(client.listReviewsForUser).toHaveBeenCalledWith(
			{ userId: 'e', pagination: { page: 1n, pageSize: 3n } },
			expect.anything()
		);
	});

	it('marks authoritative rejections as rejected and keeps unknown failures', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createReviewPhotoUpload).mockRejectedValueOnce(
			new ConnectError('limited', Code.ResourceExhausted)
		);
		vi.mocked(client.submitReview).mockRejectedValueOnce(new ConnectError('down', Code.Unavailable));
		// A temporary limit is a retryable ReviewLimitedError, not a rejection.
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1 }, new AbortController().signal))
			.rejects.toBeInstanceOf(ReviewLimitedError);
		await expect(api.submit({ from: 'r', to: 'e', orderId: 'o', rating: 1, comment: 'x', photoIds: [] }, new AbortController().signal))
			.rejects.not.toBeInstanceOf(ReviewRejectedError);
	});

	it('maps AlreadyExists to the duplicate-review error, still a rejection', async () => {
		const { client, api } = scenario();
		vi.mocked(client.submitReview).mockRejectedValueOnce(errorWithDetail('REVIEW_ALREADY_EXISTS', 'You already reviewed this request.'));
		const error = await api.submit({ from: 'r', to: 'e', orderId: 'o', rating: 1, comment: 'x', photoIds: [] }, new AbortController().signal)
			.catch((cause: unknown) => cause);
		expect(error).toBeInstanceOf(ReviewAlreadyExistsError);
		expect(error).toBeInstanceOf(ReviewRejectedError);
	});

	it('lists received reviews through the self-scoped endpoint', async () => {
		const { client, api } = scenario();
		const page = await api.listReceived({ page: 2, pageSize: 10 }, new AbortController().signal);
		expect(page.reviews.map((review) => review.id)).toEqual(['review-2']);
		expect(page.totalPages).toBe(2);
		expect(client.listMyReviewsReceived).toHaveBeenCalledWith(
			{ pagination: { page: 2n, pageSize: 10n } },
			expect.anything()
		);
	});

	it('lists written reviews through the self-scoped endpoint', async () => {
		const { client, api } = scenario();
		const page = await api.listMine({ page: 1, pageSize: 10 }, new AbortController().signal);
		expect(page.reviews.map((review) => review.id)).toEqual(['review-3']);
		expect(page.totalItems).toBe(1);
		expect(client.listMyReviews).toHaveBeenCalledWith(
			{ pagination: { page: 1n, pageSize: 10n } },
			expect.anything()
		);
	});

	it('keeps live and removed orders distinct in the batched lookup', async () => {
		const { client, api } = scenario();
		vi.mocked(client.listMyReviewsForOrders).mockResolvedValueOnce({ reviewedOrderIds: ['o1'], removedOrderIds: ['o2'] } as never);
		const statuses = await api.listReviewOrderStatuses(['o1', 'o2', 'o3'], new AbortController().signal);
		expect(statuses).toEqual({ reviewedOrderIds: ['o1'], removedOrderIds: ['o2'] });
		expect(client.listMyReviewsForOrders).toHaveBeenCalledWith({ orderIds: ['o1', 'o2', 'o3'] }, expect.anything());
	});

	it('fetches the caller review for one order; absent stays null', async () => {
		const { client, api } = scenario();
		vi.mocked(client.getMyReviewForOrder).mockResolvedValueOnce({
			review: {
				id: 'r1', reviewerId: 'r', revieweeId: 'e', reviewerName: 'R', revieweeName: 'E',
				orderId: 'o1', rating: 5, comment: 'great',
				createdAt: { seconds: 1700000000n, nanos: 0 }, photos: []
			}
		} as never);
		const review = await api.getMyReviewForOrder('o1', new AbortController().signal);
		expect(review?.rating).toBe(5);
		expect(review?.comment).toBe('great');
		vi.mocked(client.getMyReviewForOrder).mockResolvedValueOnce({ review: undefined } as never);
		await expect(api.getMyReviewForOrder('o1', new AbortController().signal)).resolves.toBeNull();
	});

	it('treats a not-found review as null, not an error', async () => {
		// The backend reports "not reviewed yet" as NotFound/REVIEW_NOT_FOUND;
		// the UI must see the same null it sees for an empty response.
		const { client, api } = scenario();
		vi.mocked(client.getMyReviewForOrder).mockRejectedValueOnce(
			new ConnectError('Review not found.', Code.NotFound, undefined, [
				{
					desc: ErrorDetailSchema,
					value: { reason: 'REVIEW_NOT_FOUND', message: 'Review not found.', fieldViolations: [] }
				}
			])
		);
		await expect(api.getMyReviewForOrder('o1', new AbortController().signal)).resolves.toBeNull();
	});

	it('keeps a removed review distinct from an unreviewed order, including duplicate recovery', async () => {
		const { client, api } = scenario();
		const removed = new ConnectError('Your review was removed.', Code.FailedPrecondition, undefined, [
			{ desc: ErrorDetailSchema, value: { reason: 'REVIEW_REMOVED', message: 'Your review was removed.', fieldViolations: [] } }
		]);
		vi.mocked(client.getMyReviewForOrder).mockRejectedValueOnce(removed);
		await expect(api.getMyReviewForOrder('o1', new AbortController().signal)).rejects.toBeInstanceOf(ReviewRemovedError);

		vi.mocked(client.submitReview).mockRejectedValueOnce(errorWithDetail('REVIEW_ALREADY_EXISTS', 'Already reviewed.'));
		await expect(api.submit({ from: 'r', to: 'e', orderId: 'o1', rating: 5, comment: 'replacement', photoIds: [] }, new AbortController().signal))
			.rejects.toBeInstanceOf(ReviewAlreadyExistsError);
		vi.mocked(client.getMyReviewForOrder).mockRejectedValueOnce(removed);
		await expect(api.getMyReviewForOrder('o1', new AbortController().signal)).rejects.toMatchObject({ reason: 'REVIEW_REMOVED' });
	});

	it('does not turn a lookup outage into an unreviewed order', async () => {
		const { client, api } = scenario();
		const outage = new ConnectError('Unavailable', Code.Unavailable);
		vi.mocked(client.getMyReviewForOrder).mockRejectedValueOnce(outage);
		await expect(api.getMyReviewForOrder('o1', new AbortController().signal)).rejects.toBe(outage);
	});

	it('maps backend reasons to retryable and permanent outcomes', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createReviewPhotoUpload).mockRejectedValueOnce(
			errorWithDetail('UPLOAD_TEMPORARILY_UNAVAILABLE', 'Photo uploads are temporarily unavailable. Try again, or submit your review without photos.')
		);
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1 }, new AbortController().signal))
			.rejects.toBeInstanceOf(ReviewLimitedError);
		vi.mocked(client.createReviewPhotoUpload).mockRejectedValueOnce(
			errorWithDetail('UPLOAD_UNAVAILABLE', 'Photo uploads are not available. You can still submit your review without photos.')
		);
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1 }, new AbortController().signal))
			.rejects.toBeInstanceOf(ReviewRejectedError);
	});

	it('names the offending photo when the backend cannot attach it', async () => {
		const { client, api } = scenario();
		const photoId = '0b8a4b5e-6c1d-4a2b-9e3f-1a2b3c4d5e6f';
		vi.mocked(client.submitReview).mockRejectedValueOnce(
			errorWithDetail('PHOTO_NOT_READY', 'Photo upload is missing; upload it again.', [photoId])
		);
		const error = await api.submit({ from: 'r', to: 'e', orderId: 'o', rating: 1, comment: 'x', photoIds: [photoId] }, new AbortController().signal)
			.catch((cause: unknown) => cause);
		expect(error).toBeInstanceOf(ReviewNotReadyError);
		expect((error as ReviewNotReadyError).photoIds).toEqual([photoId]);
		expect((error as ReviewNotReadyError).message).toBe('Photo upload is missing; upload it again.');
	});

	it('does not hand out a grant when the response is incomplete', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createReviewPhotoUpload).mockResolvedValueOnce({ photoId: '', uploadUrl: '' } as never);
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1 }, new AbortController().signal))
			.rejects.not.toBeInstanceOf(ReviewRejectedError);
	});
});

it('refreshes a photo grant with the supplied cancellation signal and maps expiry', async () => {
	const { client, api } = scenario();
	const signal = new AbortController().signal;
	expect(await api.getPhotoDownload('photo-1', signal)).toEqual({
		id: 'photo-1', url: 'https://storage.invalid/fresh', contentType: 'image/png', expiresAt: 1700003600000
	});
	expect(client.getReviewPhotoDownload).toHaveBeenCalledWith({ photoId: 'photo-1' }, { signal, timeoutMs: 15000 });
	await expect(api.getPhotoDownload('different-id', signal)).rejects.toThrow('unknown');
});
