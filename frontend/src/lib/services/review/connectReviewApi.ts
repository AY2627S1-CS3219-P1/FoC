import type { Client } from '@connectrpc/connect';
import { Code, ConnectError } from '@connectrpc/connect';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import { ReviewService, type Review as ProtoReview } from '$lib/gen/user/review/v1/review_pb';
import { parseRpcError } from '$lib/services/rpc/errors';
import {
	ReviewLimitedError,
	ReviewNotReadyError,
	ReviewRejectedError,
	ReviewAlreadyExistsError,
	ReviewRemovedError,
	type ReviewOrderStatuses,
	type ReviewApi,
	type ReviewPage,
	type ReviewPhotoGrant,
	type ReviewPhotoRecord,
	type ReviewRecord,
	type SubmitReviewInput
} from './reviewApi.ts';

type ReviewClient = Client<typeof ReviewService>;

// Authentication, cancellation, deadlines and transport failures can hide a
// committed result. Only explicit application rejection releases saved input;
// the backend dictates the message via its ErrorDetail.
function rethrow(cause: unknown): never {
	if (cause instanceof ConnectError) {
		if (cause.code === Code.ResourceExhausted) {
			// A temporary limit is retryable: keep the photo selected and let the
			// controller leave it in a retryable state.
			throw new ReviewLimitedError(
				'Photo uploads are temporarily limited. Keep this photo and try again later.'
			);
		}
		const parsed = parseRpcError(cause);
		if (parsed?.reason === 'UPLOAD_TEMPORARILY_UNAVAILABLE') {
			throw new ReviewLimitedError(parsed.message);
		}
		if (parsed?.reason === 'PHOTO_NOT_READY') {
			throw new ReviewNotReadyError(parsed.message, {
				reason: parsed.reason,
				photoIds: parsed.photoIds
			});
		}
		if (parsed?.reason === 'REVIEW_REMOVED') {
			throw new ReviewRemovedError(parsed.message);
		}
		if (parsed?.reason === 'REVIEW_ALREADY_EXISTS') {
			throw new ReviewAlreadyExistsError(parsed.message);
		}
		if (parsed?.reason === 'UPLOAD_UNAVAILABLE') {
			throw new ReviewRejectedError(parsed.message, { reason: parsed.reason });
		}
		if (
			parsed?.reason === 'INVALID_REVIEW' ||
			parsed?.reason === 'INVALID_PHOTO' ||
			parsed?.reason === 'PHOTO_NOT_STAGED'
		) {
			throw new ReviewRejectedError(parsed.message, { reason: parsed.reason });
		}
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

	async getPhotoDownload(photoId: string, signal: AbortSignal): Promise<ReviewPhotoRecord> {
		const response = await this.#client.getReviewPhotoDownload({ photoId }, { signal, timeoutMs: 15000 });
		const photo = response.photo;
		if (!photo || photo.id !== photoId || !photo.url || !photo.expiresAt) {
			throw new Error('Photo download grant result is unknown');
		}
		return {
			id: photo.id,
			url: photo.url,
			contentType: photo.contentType,
			expiresAt: timestampDate(photo.expiresAt).getTime()
		};
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

	async listReceived(
		page: { page: number; pageSize: number },
		signal: AbortSignal
	): Promise<ReviewPage> {
		try {
			const response = await this.#client.listMyReviewsReceived(
				{ pagination: { page: BigInt(page.page), pageSize: BigInt(page.pageSize) } },
				{ signal, timeoutMs: 15000 }
			);
			return {
				reviews: response.reviews.map(toReview),
				totalItems: Number(response.pagination?.totalItems ?? 0n),
				totalPages: Number(response.pagination?.totalPages ?? 0n)
			};
		} catch (cause) {
			rethrow(cause);
		}
	}

	async listMine(
		page: { page: number; pageSize: number },
		signal: AbortSignal
	): Promise<ReviewPage> {
		try {
			const response = await this.#client.listMyReviews(
				{ pagination: { page: BigInt(page.page), pageSize: BigInt(page.pageSize) } },
				{ signal, timeoutMs: 15000 }
			);
			return {
				reviews: response.reviews.map(toReview),
				totalItems: Number(response.pagination?.totalItems ?? 0n),
				totalPages: Number(response.pagination?.totalPages ?? 0n)
			};
		} catch (cause) {
			rethrow(cause);
		}
	}

	async listReviewOrderStatuses(orderIds: string[], signal: AbortSignal): Promise<ReviewOrderStatuses> {
		try {
			const response = await this.#client.listMyReviewsForOrders({ orderIds }, { signal, timeoutMs: 15000 });
			return {
				reviewedOrderIds: [...response.reviewedOrderIds],
				removedOrderIds: [...response.removedOrderIds]
			};
		} catch (cause) {
			rethrow(cause);
		}
	}

	async getMyReviewForOrder(orderId: string, signal: AbortSignal): Promise<ReviewRecord | null> {
		try {
			const response = await this.#client.getMyReviewForOrder({ orderId }, { signal, timeoutMs: 15000 });
			return response.review ? toReview(response.review) : null;
		} catch (cause) {
			// An unreviewed order is a normal state, not a failure: the backend
			// reports it as NotFound/REVIEW_NOT_FOUND.
			if (
				cause instanceof ConnectError &&
				(cause.code === Code.NotFound || parseRpcError(cause)?.reason === 'REVIEW_NOT_FOUND')
			) {
				return null;
			}
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

	async listForUser(
		userId: string,
		page: { page: number; pageSize: number },
		signal: AbortSignal
	): Promise<ReviewPage> {
		try {
			const response = await this.#client.listReviewsForUser(
				{
					userId,
					pagination: { page: BigInt(page.page), pageSize: BigInt(page.pageSize) }
				},
				{ signal, timeoutMs: 15000 }
			);
			return {
				reviews: response.reviews.map(toReview),
				totalItems: Number(response.pagination?.totalItems ?? 0n),
				totalPages: Number(response.pagination?.totalPages ?? 0n)
			};
		} catch (cause) {
			rethrow(cause);
		}
	}
}
