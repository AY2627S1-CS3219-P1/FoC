import type { ModerationStatus } from '$lib/gen/user/review/v1/review_pb';

// The adapter marks only authoritative server rejections as Rejected.
// Transport failures and ambiguous responses remain Failed/unknown.
export class ReviewRejectedError extends Error {
	readonly reason?: string;
	readonly photoIds: string[];

	constructor(message: string, options?: { reason?: string; photoIds?: string[] }) {
		super(message);
		this.reason = options?.reason;
		this.photoIds = options?.photoIds ?? [];
	}
}

// A photo the backend could not attach: it must be removed and re-added under
// a fresh grant, not blindly resubmitted.
export class ReviewNotReadyError extends ReviewRejectedError {}

// The request already carries a review. For submit this usually means a prior
// attempt committed despite an ambiguous response, so the review exists and
// must not be resubmitted.
export class ReviewAlreadyExistsError extends ReviewRejectedError {
	constructor(message: string) {
		super(message, { reason: 'REVIEW_ALREADY_EXISTS' });
	}
}

// Moderation removed the caller's review. Its content stays hidden and the
// order cannot receive a replacement review from this caller.
export class ReviewRemovedError extends ReviewRejectedError {
	constructor(message: string) {
		super(message, { reason: 'REVIEW_REMOVED' });
	}
}

export type ReviewOrderStatuses = {
	reviewedOrderIds: string[];
	removedOrderIds: string[];
};

// A temporary server-side limit (e.g. upload quota). The photo stays selected
// and a later retry can succeed; never treated as a permanent rejection.
export class ReviewLimitedError extends Error {}

export type ReviewPhotoGrant = {
	photoId: string;
	uploadUrl: string;
	uploadHeaders: Record<string, string>;
};

// One grant per photo; the browser then PUTs the bytes straight to storage.
export interface ReviewPhotoUploadAPI {
	createPhotoUpload(input: { contentType: string; sizeBytes: number }, signal: AbortSignal): Promise<ReviewPhotoGrant>;
}

export type ReviewPhotoRecord = {
	id: string;
	/** Short-lived pre-signed GET URL minted per response; never cache it. */
	url: string;
	contentType: string;
	expiresAt: number;
};

export type ReviewRecord = {
	moderationStatus?: ModerationStatus;
	id: string;
	reviewerId: string;
	reviewerName: string;
	revieweeId: string;
	revieweeName: string;
	orderId: string;
	rating: number;
	comment: string;
	createdAt: number;
	photos: ReviewPhotoRecord[];
};

export type SubmitReviewInput = {
	// Must be the authenticated caller; impersonation is rejected server-side.
	from: string;
	to: string;
	orderId: string;
	rating: number;
	comment: string;
	// IDs returned by createPhotoUpload, in the order the user arranged them.
	photoIds: string[];
};

export type ReviewPage = {
	reviews: ReviewRecord[];
	totalItems: number;
	totalPages: number;
};

/** A review the caller wrote that an administrator removed outside a report. */
export type ReviewRemovalRecord = {
	id: string;
	reviewId: string;
	revieweeId: string;
	orderId: string;
	reason: string;
	createdAt: number;
};

export type ReviewRemovalPage = {
	removals: ReviewRemovalRecord[];
	totalItems: number;
	totalPages: number;
};

export interface ReviewApi extends ReviewPhotoUploadAPI {
	submit(input: SubmitReviewInput, signal: AbortSignal): Promise<void>;
	listForUser(
		userId: string,
		page: { page: number; pageSize: number },
		signal: AbortSignal
	): Promise<ReviewPage>;
	/** Live and removed reviews for the requested orders, scoped to the caller. */
	listReviewOrderStatuses(orderIds: string[], signal: AbortSignal): Promise<ReviewOrderStatuses>;
	/** A page of reviews the caller received. */
	listReceived(page: { page: number; pageSize: number }, signal: AbortSignal): Promise<ReviewPage>;
	/** A page of reviews the caller wrote. */
	listMine(page: { page: number; pageSize: number }, signal: AbortSignal): Promise<ReviewPage>;
	/** Live review or null when unreviewed; throws ReviewRemovedError after removal. */
	getMyReviewForOrder(orderId: string, signal: AbortSignal): Promise<ReviewRecord | null>;
	/**
	 * One review for a report page. Administrators may read removed reviews;
	 * participants only live ones.
	 */
	getReview(id: string, signal: AbortSignal): Promise<{ review: ReviewRecord; removed: boolean }>;
	/** The caller's reviews removed outside a report, which they may appeal. */
	listMyRemovals(page: { page: number; pageSize: number }, signal: AbortSignal): Promise<ReviewRemovalPage>;
}
