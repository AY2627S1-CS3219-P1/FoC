import type { Client } from '@connectrpc/connect';
import { Code, ConnectError } from '@connectrpc/connect';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import { ReviewService, type Review as ProtoReview } from '$lib/gen/user/review/v1/review_pb';
import {
	ReviewRejectedError,
	type ReviewApi,
	type ReviewPhotoGrant,
	type ReviewRecord,
	type SubmitReviewInput
} from './reviewApi.ts';

type ReviewClient = Client<typeof ReviewService>;

// Authentication, cancellation, deadlines and transport failures can hide a
// committed result. Only explicit application rejection releases saved input.
function rethrow(cause: unknown): never {
	if (cause instanceof ConnectError && [
		Code.InvalidArgument, Code.FailedPrecondition, Code.NotFound,
		Code.AlreadyExists, Code.ResourceExhausted
	].includes(cause.code)) {
		throw new ReviewRejectedError(cause.code === Code.ResourceExhausted
			? 'Photo uploads are temporarily limited. Keep this photo and try again later.'
			: 'This action could not be completed. Check the review details and photos, then try again.');
	}
	throw cause;
}

function toReview(review: ProtoReview): ReviewRecord {
	return {
		id: review.id,
		reviewerId: review.reviewerId,
		reviewerName: review.reviewerName,
		revieweeId: review.revieweeId,
		revieweeName: review.revieweeName,
		orderId: review.orderId,
		rating: review.rating,
		comment: review.comment,
		createdAt: review.createdAt ? timestampDate(review.createdAt).getTime() : 0,
		photos: review.photos.map((photo) => ({
			id: photo.id,
			url: photo.url,
			contentType: photo.contentType,
			expiresAt: photo.expiresAt ? timestampDate(photo.expiresAt).getTime() : 0
		}))
	};
}

export class ConnectReviewApi implements ReviewApi {
	readonly #client: ReviewClient;

	constructor(client: ReviewClient) {
		this.#client = client;
	}

	async createPhotoUpload(input: { contentType: string; sizeBytes: number }, signal: AbortSignal): Promise<ReviewPhotoGrant> {
		try {
			const response = await this.#client.createReviewPhotoUpload(
				{ contentType: input.contentType, sizeBytes: BigInt(input.sizeBytes) },
				{ signal, timeoutMs: 15000 }
			);
			if (!response.photoId || !response.uploadUrl) throw new Error('Photo upload grant result is unknown');
			return { photoId: response.photoId, uploadUrl: response.uploadUrl, uploadHeaders: response.uploadHeaders };
		} catch (cause) {
			rethrow(cause);
		}
	}

	async submit(input: SubmitReviewInput, signal: AbortSignal): Promise<void> {
		try {
			await this.#client.submitReview(
				{
					from: input.from,
					to: input.to,
					orderId: input.orderId,
					submission: { rating: input.rating, comment: input.comment },
					photoIds: [...input.photoIds]
				},
				{ signal, timeoutMs: 15000 }
			);
		} catch (cause) {
			rethrow(cause);
		}
	}

	async listForUser(userId: string, signal: AbortSignal): Promise<ReviewRecord[]> {
		try {
			const response = await this.#client.listReviewsForUser({ userId }, { signal, timeoutMs: 15000 });
			return response.reviews.map(toReview);
		} catch (cause) {
			rethrow(cause);
		}
	}
}
