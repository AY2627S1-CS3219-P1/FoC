import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { Code, ConnectError } from '@connectrpc/connect';
import { ReviewService as ReviewServiceDefinition } from '$lib/gen/user/review/v1/review_pb';
import { ReviewAlreadyExistsError, ReviewLimitedError, ReviewNotReadyError, ReviewRejectedError } from './reviewApi.ts';
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
		createReviewPhotoUpload: vi.fn(async () => ({
			photoId: 'photo-1', uploadUrl: 'https://storage.invalid/upload', uploadHeaders: { 'x-test': '1' }, expiresAt: undefined
		})),
		submitReview: vi.fn(async () => ({})),
		listMyReviewsForOrders: vi.fn(async () => ({ reviewedOrderIds: [] })),
		getMyReviewForOrder: vi.fn(async () => ({ review: undefined })),
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

	it('returns the reviewed subset for the batched lookup', async () => {
		const { client, api } = scenario();
		vi.mocked(client.listMyReviewsForOrders).mockResolvedValueOnce({ reviewedOrderIds: ['o1'] } as never);
		const ids = await api.listReviewedOrderIds(['o1', 'o2'], new AbortController().signal);
		expect(ids).toEqual(['o1']);
		expect(client.listMyReviewsForOrders).toHaveBeenCalledWith({ orderIds: ['o1', 'o2'] }, expect.anything());
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
