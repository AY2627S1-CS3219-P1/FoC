import { describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { EvidenceUploadGrantSchema } from '$lib/gen/user/evidence/v1/evidence_pb';
import { ReviewLimitedError, ReviewRejectedError } from '$lib/services/review/reviewApi';
import { asPhotoUploadApi } from './evidence.ts';
import { describeModerationError } from './errors.ts';
import { boundedText, responseSchema } from './schemas.ts';

const signal = new AbortController().signal;

describe('asPhotoUploadApi', () => {
	it('exposes the evidence ID as the staged photo ID', async () => {
		const grant = create(EvidenceUploadGrantSchema, {
			evidenceId: 'evidence-id',
			uploadUrl: 'https://storage.test/put',
			uploadHeaders: { 'content-type': 'image/png' }
		});
		const createUpload = vi.fn(async () => grant);
		const api = asPhotoUploadApi(createUpload);

		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 10 }, signal)).resolves.toEqual({
			photoId: 'evidence-id',
			uploadUrl: 'https://storage.test/put',
			uploadHeaders: { 'content-type': 'image/png' }
		});
		expect(createUpload).toHaveBeenCalledWith({ contentType: 'image/png', sizeBytes: 10 }, signal);
	});

	it('keeps a temporarily limited image retryable', async () => {
		const api = asPhotoUploadApi(async () => {
			throw new ConnectError('slow down', Code.ResourceExhausted);
		});
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 10 }, signal)).rejects.toBeInstanceOf(
			ReviewLimitedError
		);
	});

	it('marks an image the server rejects as unusable', async () => {
		const api = asPhotoUploadApi(async () => {
			throw new ConnectError('unsupported type', Code.InvalidArgument);
		});
		await expect(api.createPhotoUpload({ contentType: 'image/gif', sizeBytes: 10 }, signal)).rejects.toBeInstanceOf(
			ReviewRejectedError
		);
	});

	it('leaves unknown failures untouched', async () => {
		const failure = new ConnectError('lost', Code.DeadlineExceeded);
		const api = asPhotoUploadApi(async () => {
			throw failure;
		});
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 10 }, signal)).rejects.toBe(failure);
	});

	it('rejects an incomplete grant', async () => {
		const api = asPhotoUploadApi(async () => undefined);
		await expect(api.createPhotoUpload({ contentType: 'image/png', sizeBytes: 10 }, signal)).rejects.toThrow('unknown');
	});
});

describe('describeModerationError', () => {
	it('shows the backend message for client errors', () => {
		const cause = new ConnectError('warning is no longer active', Code.FailedPrecondition);
		expect(describeModerationError(cause, 'submit the appeal')).toBe(
			'Could not submit the appeal: warning is no longer active.'
		);
	});

	it('hides server failures behind a retry message', () => {
		expect(describeModerationError(new ConnectError('boom', Code.Internal), 'close the report')).toBe(
			'Could not close the report. Please try again.'
		);
		expect(describeModerationError(new Error('offline'), 'close the report')).toBe(
			'Could not close the report. Please try again.'
		);
	});
});

describe('moderation form schemas', () => {
	it('counts characters rather than UTF-16 units and trims', () => {
		const schema = boundedText(2, 'text');
		expect(schema.safeParse('😀😀').success).toBe(true);
		expect(schema.safeParse('😀😀😀').success).toBe(false);
		expect(schema.safeParse('   ').success).toBe(false);
	});

	it('requires a response body', () => {
		expect(responseSchema.safeParse({ body: 'My side' }).success).toBe(true);
		expect(responseSchema.safeParse({ body: '' }).success).toBe(false);
	});
});
