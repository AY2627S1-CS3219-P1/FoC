import { ReviewLimitedError, ReviewRejectedError, type ReviewPhotoGrant, type ReviewPhotoUploadAPI } from '$lib/services/review/reviewApi';

export type ReviewPhotoOutcome = 'Not Submitted' | 'Pending' | 'Succeeded' | 'Rejected' | 'Failed';
export type ReviewPhotoRow = {
	id: string;
	file: File;
	photoId?: string;
	uploadRequested?: boolean;
	outcome: ReviewPhotoOutcome;
	message: string;
};
export type ReviewPhotosSnapshot = {
	files: ReviewPhotoRow[];
	outcome: ReviewPhotoOutcome;
	message: string;
	busy: boolean;
};

const types = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/heic', 'image/heif']);
const maxPhotos = 5;
const maxBytes = 10_000_000;
const apiTimeoutMs = 15_000;
// Allow 64 KiB/s plus API-sized overhead for a bounded direct upload.
const uploadBytesPerSecond = 64 * 1024;

type PutPhoto = (grant: ReviewPhotoGrant, file: File, signal: AbortSignal) => Promise<void>;

async function putPhoto(grant: ReviewPhotoGrant, file: File, signal: AbortSignal): Promise<void> {
	const response = await fetch(grant.uploadUrl, {
		method: 'PUT',
		signal,
		headers: grant.uploadHeaders,
		body: file
	});
	if (!response.ok) throw new Error('Upload response was not successful');
}

/**
 * Stages up to five photos for one review. Each staged photo gets its own
 * grant and is PUT straight to storage; there is no verify or abandon RPC, so
 * a removed photo is only dropped locally and its staged upload is reaped by
 * the backend cleanup.
 */
export class ReviewPhotosController {
	private files: ReviewPhotoRow[] = [];
	private outcome: ReviewPhotoOutcome = 'Not Submitted';
	private message = '';
	private busy = false;
	private disposed = false;
	private lifetime = new AbortController();

	constructor(
		private api: ReviewPhotoUploadAPI,
		private changed: (snapshot: ReviewPhotosSnapshot) => void,
		private put: PutPhoto = putPhoto
	) {}

	snapshot(): ReviewPhotosSnapshot {
		return {
			files: this.files.map((file) => ({ ...file })),
			outcome: this.outcome,
			message: this.message,
			busy: this.busy
		};
	}

	private publish(): void {
		if (!this.disposed) this.changed(this.snapshot());
	}

	dispose(): void {
		this.disposed = true;
		this.lifetime.abort();
	}

	private async bounded<T>(operation: (signal: AbortSignal) => Promise<T>, timeoutMs = apiTimeoutMs): Promise<T> {
		const signal = AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(timeoutMs)]);
		signal.throwIfAborted();
		return new Promise<T>((resolve, reject) => {
			const abort = () => reject(signal.reason);
			signal.addEventListener('abort', abort, { once: true });
			operation(signal).then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
		});
	}

	addFiles(selected: File[]): void {
		if (this.busy || this.disposed) return;
		if (this.files.length + selected.length > maxPhotos) {
			this.message = 'Attach at most 5 photos.';
			this.publish();
			return;
		}
		for (const file of selected) {
			const valid = types.has(file.type) && file.size >= 1 && file.size <= maxBytes;
			this.files.push({
				id: crypto.randomUUID(),
				file,
				outcome: valid ? 'Not Submitted' : 'Rejected',
				message: valid
					? ''
					: 'Replace this file with a JPEG, PNG, WebP, HEIC or HEIF photo, up to 10MB.'
			});
		}
		this.publish();
	}

	// Removal is always local: staged uploads without a review are reaped by the
	// backend, so their photo IDs must simply never reach submitReview.
	removeFile(id: string): void {
		if (this.disposed) return;
		const row = this.files.find((file) => file.id === id);
		if (!row || row.outcome === 'Pending') return;
		this.files = this.files.filter((file) => file.id !== id);
		this.message = row.uploadRequested ? 'Photo removed. It will not be attached to your review.' : '';
		this.publish();
	}

	// Photo IDs in display order, so the review keeps the arranged sequence.
	photoIds(): string[] {
		return this.files
			.filter((row) => row.outcome === 'Succeeded' && row.photoId)
			.map((row) => row.photoId!);
	}

	async upload(): Promise<void> {
		if (this.busy || this.disposed) return;
		this.busy = true;
		this.outcome = 'Pending';
		this.message = 'Uploading photos…';
		this.publish();
		try {
			for (const row of this.files) {
				if (this.disposed) break;
				// A row removed mid-pass must not receive a new upload.
				if (!this.files.includes(row)) continue;
				if (row.outcome === 'Succeeded' || row.outcome === 'Rejected') continue;
				row.outcome = 'Pending';
				row.message = 'Uploading photo…';
				this.publish();
				try {
					row.uploadRequested = true;
					const grant = await this.bounded((signal) => this.api.createPhotoUpload({
						contentType: row.file.type,
						sizeBytes: row.file.size
					}, signal));
					row.photoId = grant.photoId;
					const uploadTimeoutMs = apiTimeoutMs + Math.ceil(row.file.size / uploadBytesPerSecond) * 1000;
					await this.bounded((signal) => this.put(grant, row.file, signal), uploadTimeoutMs);
					row.outcome = 'Succeeded';
					row.message = '';
				} catch (cause) {
					// A temporary limit leaves the row retryable ('Failed'); only an
					// authoritative server rejection is permanent.
					row.outcome = cause instanceof ReviewRejectedError ? 'Rejected' : 'Failed';
					row.message = cause instanceof ReviewLimitedError
						? cause.message
						: cause instanceof ReviewRejectedError
							? cause.message
							: 'Could not upload this photo. Try again or remove it.';
				}
				this.publish();
			}
			this.outcome = 'Not Submitted';
			this.message = this.describeFiles();
		} finally {
			this.busy = false;
			this.publish();
		}
	}

	private describeFiles(): string {
		const selected = this.files.filter((row) => row.outcome !== 'Rejected');
		if (selected.length === 0) return '';
		if (selected.every((row) => row.outcome === 'Succeeded')) {
			return 'Photos ready. Submit your review when you are done.';
		}
		if (selected.some((row) => row.outcome === 'Pending')) {
			return 'Still uploading photos. Wait here or submit later.';
		}
		return 'Some photos did not upload. Retry or remove the failed photos before submitting.';
	}
}
