// The adapter marks only authoritative server rejections as Rejected.
// Transport failures and ambiguous responses remain Failed/unknown.
export class ReviewRejectedError extends Error {}

// The request already carries a review. For submit this usually means a prior
// attempt committed despite an ambiguous response, so the review exists and
// must not be resubmitted.
export class ReviewAlreadyExistsError extends ReviewRejectedError {}

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

export interface ReviewApi extends ReviewPhotoUploadAPI {
	submit(input: SubmitReviewInput, signal: AbortSignal): Promise<void>;
	listForUser(
		userId: string,
		page: { page: number; pageSize: number },
		signal: AbortSignal
	): Promise<ReviewPage>;
}
