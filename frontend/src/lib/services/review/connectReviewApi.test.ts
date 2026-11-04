import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { Code, ConnectError } from '@connectrpc/connect';
import { ReviewService as ReviewServiceDefinition } from '$lib/gen/user/review/v1/review_pb';
import { ReviewRejectedError } from './reviewApi.ts';
import { ConnectReviewApi } from './connectReviewApi.ts';

function scenario() {
	const client = {
		createReviewPhotoUpload: vi.fn(async () => ({
			photoId: 'photo-1', uploadUrl: 'https://storage.invalid/upload', uploadHeaders: { 'x-test': '1' }, expiresAt: undefined
		})),
		submitReview: vi.fn(async () => ({})),
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
		const { api } = scenario();
		const reviews = await api.listForUser('e', new AbortController().signal);
		expect(reviews).toHaveLength(1);
		expect(reviews[0]).toMatchObject({
			id: 'review-1', rating: 4, comment: 'Great',
			createdAt: 1700000000000,
			photos: [{ id: 'photo-1', url: 'https://storage.invalid/get', contentType: 'image/png', expiresAt: 1700003600000 }]
		});
	});

	it('marks authoritative rejections as rejected and keeps unknown failures', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createReviewPhotoUpload).mockRejectedValueOnce(
			new ConnectError('limited', Code.ResourceExhausted)
		);
		vi.mocked(client.submitReview).mockRejectedValueOnce(new ConnectError('down', Code.Unavailable));
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1 }, new AbortController().signal))
			.rejects.toBeInstanceOf(ReviewRejectedError);
		await expect(api.submit({ from: 'r', to: 'e', orderId: 'o', rating: 1, comment: 'x', photoIds: [] }, new AbortController().signal))
			.rejects.not.toBeInstanceOf(ReviewRejectedError);
	});

	it('does not hand out a grant when the response is incomplete', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createReviewPhotoUpload).mockResolvedValueOnce({ photoId: '', uploadUrl: '' } as never);
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 1 }, new AbortController().signal))
			.rejects.not.toBeInstanceOf(ReviewRejectedError);
	});
});
