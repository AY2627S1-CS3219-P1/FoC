import { describe, expect, it, vi } from 'vitest';
import { ReviewPhotosController, putPhoto } from './controller';
import { OversizedConversionError } from '$lib/features/image-upload/heic';
import { ReviewRejectedError, type ReviewPhotoGrant, type ReviewPhotoUploadAPI } from '$lib/services/review/reviewApi';

// The review flow has no verify or abandon RPC: a grant and a PUT decide a
// photo, and removed photos are only ever dropped locally.
function scenario(putImpl: (grant: ReviewPhotoGrant, file: File, signal: AbortSignal) => Promise<void> = async () => {}) {
	const api: ReviewPhotoUploadAPI = {
		createPhotoUpload: vi.fn(async () => ({
			photoId: `photo-${crypto.randomUUID()}`,
			uploadUrl: 'https://storage.invalid/upload',
			uploadHeaders: {}
		}))
	};
	const put = vi.fn(putImpl);
	const controller = new ReviewPhotosController(api, () => {}, put, false);
	return { api, put, controller };
}

const photo = (name = 'review.png') => new File(['photo'], name, { type: 'image/png' });

describe('review photo auto-upload', () => {
	it('starts uploading as soon as files are added', async () => {
		const api: ReviewPhotoUploadAPI = {
			createPhotoUpload: vi.fn(async () => ({
				photoId: `photo-${crypto.randomUUID()}`,
				uploadUrl: 'https://storage.invalid/upload',
				uploadHeaders: {}
			}))
		};
		const put = vi.fn(async () => {});
		const controller = new ReviewPhotosController(api, () => {}, put, true);
		controller.addFiles([photo('auto.png')]);
		await vi.waitFor(() => expect(controller.snapshot().files[0].outcome).toBe('Succeeded'));
		expect(api.createPhotoUpload).toHaveBeenCalledTimes(1);
		expect(put).toHaveBeenCalledTimes(1);
	});

	it('does not raise a group message for a successful upload', async () => {
		// The page turns any group message into a snackbar; success is already
		// visible on the row, so it must stay silent.
		const api: ReviewPhotoUploadAPI = {
			createPhotoUpload: vi.fn(async () => ({
				photoId: `photo-${crypto.randomUUID()}`,
				uploadUrl: 'https://storage.invalid/upload',
				uploadHeaders: {}
			}))
		};
		const controller = new ReviewPhotosController(api, () => {}, vi.fn(async () => {}), false);
		controller.addFiles([photo('silent.png')]);
		await controller.upload();
		expect(controller.snapshot().files[0].outcome).toBe('Succeeded');
		expect(controller.snapshot().message).toBe('');
	});
});

describe('review photo global retry', () => {
	it('retries every failed photo in a single pass', async () => {
		const { put, controller } = scenario();
		controller.addFiles([photo('a.png'), photo('b.png')]);
		put.mockRejectedValueOnce(new Error('down')).mockRejectedValueOnce(new Error('down'));
		await controller.upload();
		expect(controller.snapshot().files.map((file) => file.outcome)).toEqual(['Failed', 'Failed']);

		controller.retryFailed();
		await vi.waitFor(() =>
			expect(controller.snapshot().files.every((file) => file.outcome === 'Succeeded')).toBe(true)
		);
		// Two initial PUTs plus two retried PUTs: the retry covered both photos.
		expect(put).toHaveBeenCalledTimes(4);
	});

	it('stays idle when nothing failed', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo('ok.png')]);
		await controller.upload();
		const grants = vi.mocked(api.createPhotoUpload).mock.calls.length;

		controller.retryFailed();
		await Promise.resolve();
		expect(vi.mocked(api.createPhotoUpload).mock.calls.length).toBe(grants);
	});
});

describe('review photo not-ready handling', () => {
	it('flags the row the backend could not attach and keeps it out of the payload', async () => {
		const { controller } = scenario();
		controller.addFiles([photo('first.png'), photo('second.png')]);
		await controller.upload();
		const staleId = controller.snapshot().files[0].photoId!;

		controller.markNotReady([staleId], "This photo didn't make it. Remove and re-add it, or submit without it.");
		const snapshot = controller.snapshot();
		expect(snapshot.files[0].outcome).toBe('Rejected');
		expect(snapshot.files[0].message).toContain("Remove and re-add it");
		expect(controller.photoIds()).toEqual([snapshot.files[1].photoId]);
		// The stale group status names the dead photos instead of going quiet.
		expect(snapshot.message).toContain("can't be used");
	});

	it('leaves untouched rows alone when only one photo is flagged', async () => {
		const { controller } = scenario();
		controller.addFiles([photo(), photo()]);
		await controller.upload();
		const first = controller.snapshot().files[0].photoId!;

		controller.markNotReady([first], 'gone');
		const snapshot = controller.snapshot();
		expect(snapshot.files[1].outcome).toBe('Succeeded');
		expect(controller.photoIds()).toEqual([snapshot.files[1].photoId]);
	});
});

describe('review photo staging', () => {
	it('grants and PUTs each photo once, marking it succeeded with its photo id', async () => {
		const { api, put, controller } = scenario();
		controller.addFiles([photo('first.png'), photo('second.png')]);
		await controller.upload();
		const snapshot = controller.snapshot();
		expect(snapshot.files.map((file) => file.outcome)).toEqual(['Succeeded', 'Succeeded']);
		expect(api.createPhotoUpload).toHaveBeenCalledTimes(2);
		expect(put).toHaveBeenCalledTimes(2);
		expect(snapshot.files.every((file) => file.photoId && file.uploadRequested)).toBe(true);
		expect(controller.photoIds()).toEqual(snapshot.files.map((file) => file.photoId!));
	});

	it('keeps the display order in the submission photo ids', async () => {
		const { api, controller } = scenario();
		let next = 0;
		vi.mocked(api.createPhotoUpload).mockImplementation(async () => {
			next += 1;
			return { photoId: `photo-${next}`, uploadUrl: 'https://storage.invalid/upload', uploadHeaders: {} };
		});
		controller.addFiles([photo('a.png'), photo('b.png'), photo('c.png')]);
		await controller.upload();
		// Grants were requested top to bottom, so ids arrive in display order.
		expect(controller.photoIds()).toEqual(['photo-1', 'photo-2', 'photo-3']);
	});

	it('marks a failed PUT as failed and retries only that photo with a fresh grant', async () => {
		const { api, put, controller } = scenario();
		controller.addFiles([photo('failed.png'), photo('saved.png')]);
		put.mockRejectedValueOnce(new Error('storage down'));
		await controller.upload();
		expect(controller.snapshot().files.map((file) => file.outcome)).toEqual(['Failed', 'Succeeded']);
		expect(controller.photoIds()).toHaveLength(1);
		await controller.upload();
		// Only the failed photo was re-requested and re-PUT.
		expect(api.createPhotoUpload).toHaveBeenCalledTimes(3);
		expect(put).toHaveBeenCalledTimes(3);
		expect(controller.snapshot().files.every((file) => file.outcome === 'Succeeded')).toBe(true);
	});

	it('keeps a review valid with zero photos', () => {
		const { controller } = scenario();
		expect(controller.photoIds()).toEqual([]);
		expect(controller.snapshot().files).toHaveLength(0);
		expect(controller.snapshot().message).toBe('');
	});

	it('enforces five photos and the byte/type limit without discarding prior input', () => {
		const { controller } = scenario();
		controller.addFiles(Array.from({ length: 5 }, (_, index) => photo(`${index}.png`)));
		controller.addFiles([photo('sixth.png')]);
		expect(controller.snapshot().files).toHaveLength(5);
		expect(controller.snapshot().message).toContain('at most 5');
		const other = scenario();
		other.controller.addFiles([new File(['text'], 'not-a-photo.txt', { type: 'text/plain' })]);
		expect(other.controller.snapshot().files[0].outcome).toBe('Rejected');
		expect(other.controller.snapshot().files[0].message).toContain('10MB');
		other.controller.addFiles([new File([new Uint8Array(10_000_001)], 'big.png', { type: 'image/png' })]);
		expect(other.controller.snapshot().files[1].outcome).toBe('Rejected');
	});

	it('excludes removed photos from the submission without any server call', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo('kept.png'), photo('dropped.png')]);
		await controller.upload();
		const droppedId = controller.snapshot().files[1].photoId!;
		controller.removeFile(controller.snapshot().files[1].id);
		expect(controller.snapshot().files).toHaveLength(1);
		expect(controller.photoIds()).toEqual([controller.snapshot().files[0].photoId]);
		expect(controller.photoIds()).not.toContain(droppedId);
		// There is no abandon RPC to call; the staged upload is reaped later.
		expect(api.createPhotoUpload).toHaveBeenCalledTimes(2);
	});

	it('ignores removal of the photo whose upload is in flight', async () => {
		let started!: () => void;
		const uploading = new Promise<void>((resolve) => { started = resolve; });
		let release!: () => void;
		const { controller } = scenario((_grant, _file, _signal) => {
			started();
			return new Promise<void>((resolve) => { release = resolve; });
		});
		controller.addFiles([photo()]);
		const work = controller.upload();
		await uploading;
		const busyId = controller.snapshot().files[0].id;
		controller.removeFile(busyId);
		expect(controller.snapshot().files).toHaveLength(1);
		release();
		await work;
		expect(controller.snapshot().files[0].outcome).toBe('Succeeded');
		controller.removeFile(busyId);
		expect(controller.snapshot().files).toHaveLength(0);
	});

	it('drops rejected rows from upload passes and reports mixed results', async () => {
		const { put, controller } = scenario();
		controller.addFiles([photo('good.png'), new File(['x'], 'bad.txt', { type: 'text/plain' })]);
		put.mockRejectedValueOnce(new Error('storage down'));
		await controller.upload();
		const snapshot = controller.snapshot();
		expect(snapshot.files.map((file) => file.outcome)).toEqual(['Failed', 'Rejected']);
		expect(snapshot.message).toContain('Retry or remove');
	});

	it('distinguishes an authoritative rejection from an unknown failure', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo('limited.png')]);
		vi.mocked(api.createPhotoUpload).mockRejectedValueOnce(new ReviewRejectedError('Photo uploads are temporarily limited. Keep this photo and try again later.'));
		await controller.upload();
		const snapshot = controller.snapshot();
		expect(snapshot.files[0].outcome).toBe('Rejected');
		expect(snapshot.files[0].message).toContain('temporarily limited');
	});
});

describe('review photo heic transcode', () => {
	const heic = () => new File([new Uint8Array(8)], 'IMG_0001.heic', { type: 'image/heic' });

	function transcodeScenario(transcode: (file: File) => Promise<File>) {
		const api: ReviewPhotoUploadAPI = {
			createPhotoUpload: vi.fn(async () => ({
				photoId: `photo-${crypto.randomUUID()}`,
				uploadUrl: 'https://storage.invalid/upload',
				uploadHeaders: {}
			}))
		};
		const put = vi.fn(async (_grant: ReviewPhotoGrant, _file: File, _signal: AbortSignal) => {});
		const controller = new ReviewPhotosController(api, () => {}, put, false, transcode);
		return { api, put, controller };
	}

	it('uploads the converted JPEG for a HEIC pick', async () => {
		const { api, put, controller } = transcodeScenario(async (file) => {
			expect(file.type).toBe('image/heic');
			return new File([new Uint8Array(4)], 'IMG_0001.jpg', { type: 'image/jpeg' });
		});
		controller.addFiles([heic()]);
		await controller.upload();
		const grant = vi.mocked(api.createPhotoUpload).mock.calls[0]![0];
		expect(grant).toEqual({ contentType: 'image/jpeg', sizeBytes: 4 });
		expect(put.mock.calls[0]![1].name).toBe('IMG_0001.jpg');
		// The row keeps the converted file, so its preview matches what reviewers get.
		const row = controller.snapshot().files[0]!;
		expect(row.file.type).toBe('image/jpeg');
		expect(row.outcome).toBe('Succeeded');
	});

	it('uploads the original HEIC bytes when conversion fails', async () => {
		const { api, put, controller } = transcodeScenario(async (file) => file);
		controller.addFiles([heic()]);
		await controller.upload();
		const grant = vi.mocked(api.createPhotoUpload).mock.calls[0]![0];
		expect(grant).toEqual({ contentType: 'image/heic', sizeBytes: 8 });
		expect(put.mock.calls[0]![1].name).toBe('IMG_0001.heic');
		expect(controller.snapshot().files[0]!.outcome).toBe('Succeeded');
	});

	it('rejects a converted photo that cannot fit the upload cap without a grant', async () => {
		const { api, put, controller } = transcodeScenario(async () => {
			throw new OversizedConversionError();
		});
		controller.addFiles([heic()]);
		await controller.upload();
		const row = controller.snapshot().files[0]!;
		expect(row.outcome).toBe('Rejected');
		expect(row.message).toContain('10MB');
		// No grant or PUT may run for bytes that can never pass the cap.
		expect(api.createPhotoUpload).not.toHaveBeenCalled();
		expect(put).not.toHaveBeenCalled();
	});

	it('does not convert files browsers can already decode', async () => {
		const transcode = vi.fn(async (file: File) => file);
		const { controller } = transcodeScenario(transcode);
		controller.addFiles([photo('keep.png'), new File(['x'], 'keep.jpg', { type: 'image/jpeg' })]);
		await controller.upload();
		expect(transcode).not.toHaveBeenCalled();
	});
});

describe('bounded review photo work', () => {
	it('cancels a stalled upload on unmount and does not start the next selected file', async () => {
		const api: ReviewPhotoUploadAPI = {
			createPhotoUpload: vi.fn(async () => ({ photoId: 'photo-1', uploadUrl: 'https://storage.invalid/upload', uploadHeaders: {} }))
		};
		let started!: () => void;
		const uploading = new Promise<void>((resolve) => { started = resolve; });
		const put = vi.fn((_grant: ReviewPhotoGrant, _file: File, signal: AbortSignal) => {
			started();
			return new Promise<void>((_resolve, reject) => {
				signal.addEventListener('abort', () => reject(signal.reason), { once: true });
			});
		});
		const changed = vi.fn();
		const managed = new ReviewPhotosController(api, changed, put, false);
		managed.addFiles([photo('first.png'), photo('second.png')]);
		const work = managed.upload();
		await uploading;
		managed.dispose();
		const previousUpdates = changed.mock.calls.length;
		await work;
		expect(api.createPhotoUpload).toHaveBeenCalledOnce();
		expect(changed).toHaveBeenCalledTimes(previousUpdates);
	});

	it('allows an 8 MB photo to finish after the API timeout', async () => {
		vi.useFakeTimers();
		const timeout = vi.spyOn(AbortSignal, 'timeout').mockImplementation((milliseconds) => {
			const controller = new AbortController();
			setTimeout(() => controller.abort(new DOMException('Timed out', 'TimeoutError')), milliseconds);
			return controller.signal;
		});
		try {
			const { controller } = scenario((_grant, _file, signal) => new Promise<void>((resolve, reject) => {
				setTimeout(resolve, 22_000);
				signal.addEventListener('abort', () => reject(signal.reason), { once: true });
			}));			controller.addFiles([new File([new Uint8Array(8_000_000)], 'phone.heic', { type: 'image/heic' })]);
			const work = controller.upload();
			await vi.advanceTimersByTimeAsync(22_000);
			await work;
			expect(controller.snapshot().files[0].outcome).toBe('Succeeded');
		} finally {
			timeout.mockRestore();
			vi.clearAllTimers();
			vi.useRealTimers();
		}
	}, 20_000);
});


describe('review photo direct upload', () => {
	it('sends object bytes without cookies or redirects', async () => {
		const file = new File(['photo'], 'photo.png', { type: 'image/png' });
		const grant = {
			photoId: 'photo-1',
			uploadUrl: 'https://storage.invalid/upload',
			uploadHeaders: { 'Content-Type': 'image/png', 'If-None-Match': '*' }
		};
		let seen: RequestInit | undefined;
		const send = (async (_url: string | URL | Request, init?: RequestInit) => {
			seen = init;
			return new Response('', { status: 200 });
		}) as typeof fetch;

		await putPhoto(grant, file, new AbortController().signal, send);

		expect(seen).toMatchObject({
			method: 'PUT',
			body: file,
			headers: grant.uploadHeaders,
			credentials: 'omit',
			redirect: 'error'
		});
	});
});
