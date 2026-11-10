import { Code, ConnectError } from '@connectrpc/connect';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import type { EvidenceUploadGrant } from '$lib/gen/user/evidence/v1/evidence_pb';
import { parseRpcError } from '$lib/services/rpc/errors';
import {
	ReviewLimitedError,
	ReviewRejectedError,
	type ReviewPhotoGrant,
	type ReviewPhotoUploadAPI
} from '$lib/services/review/reviewApi';

export type EvidenceUploadInput = { contentType: string; sizeBytes: number };

/** Requests one signed upload from the owning service (report or appeal). */
export type CreateEvidenceUpload = (
	input: EvidenceUploadInput,
	signal: AbortSignal
) => Promise<EvidenceUploadGrant | undefined>;

/**
 * Adapts an evidence grant RPC to the review photo upload API so the shared
 * PhotoUploader and ReviewPhotosController stage evidence images unchanged.
 * The controller's photo IDs are evidence IDs.
 */
export function asPhotoUploadApi(createUpload: CreateEvidenceUpload): ReviewPhotoUploadAPI {
	return {
		async createPhotoUpload(input, signal): Promise<ReviewPhotoGrant> {
			let grant: EvidenceUploadGrant | undefined;
			try {
				grant = await createUpload(input, signal);
			} catch (cause) {
				throw toUploadError(cause);
			}
			if (!grant?.evidenceId || !grant.uploadUrl) throw new Error('Evidence upload grant result is unknown');
			return { photoId: grant.evidenceId, uploadUrl: grant.uploadUrl, uploadHeaders: grant.uploadHeaders };
		}
	};
}

// A temporary limit stays retryable; only an authoritative rejection makes the
// controller mark the image as unusable. Everything else is an unknown failure.
function toUploadError(cause: unknown): unknown {
	if (!(cause instanceof ConnectError)) return cause;
	if (cause.code === Code.ResourceExhausted || cause.code === Code.Unavailable) {
		return new ReviewLimitedError('Image uploads are temporarily limited. Keep this image and try again later.');
	}
	if (cause.code === Code.InvalidArgument || cause.code === Code.FailedPrecondition) {
		const parsed = parseRpcError(cause);
		return new ReviewRejectedError(parsed?.message || 'This image cannot be used.', { reason: parsed?.reason });
	}
	return cause;
}

export function grantExpiry(grant: EvidenceUploadGrant): number {
	return grant.expiresAt ? timestampDate(grant.expiresAt).getTime() : 0;
}
