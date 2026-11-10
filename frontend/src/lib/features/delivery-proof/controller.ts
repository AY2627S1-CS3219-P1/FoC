import { ProofRejectedError, type DeliveryProofAPI, type ProofGrant } from '$lib/services/delivery-proof/api';

export type ProofOutcome = 'Not Submitted' | 'Pending' | 'Succeeded' | 'Rejected' | 'Failed';
export type ProofFile = {
	id: string;
	file: File;
	uploadFile?: File;
	proofId?: string;
	uploadRequested?: boolean;
	removalRequested?: boolean;
	outcome: ProofOutcome;
	message: string;
};
export type ProofSnapshot = {
	files: ProofFile[];
	unattended: boolean;
	outcome: ProofOutcome;
	message: string;
	locked: boolean;
	busy: boolean;
};

const types = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/heic', 'image/heif']);
const maxBytes = 10_000_000;
const apiTimeoutMs = 15_000;
// Allow 64 KiB/s plus API-sized overhead for a bounded direct upload.
const uploadBytesPerSecond = 64 * 1024;
// The server admits pixel areas below 24 MP (8192 px per side). A 4000 px long
// side keeps every aspect ratio inside that budget while shrinking uploads on
// mobile data.
const maxPixelSide = 4000;

type PutPhoto = (grant: ProofGrant, file: File, signal: AbortSignal) => Promise<void>;
type PreparePhoto = (file: File) => Promise<File>;

// preparePhoto resamples an ordinary photo into the proof pixel budget before
// the upload grant is requested. Formats the browser cannot decode (such as
// HEIC outside Safari), processing failures and already-small photos upload the
// original bytes.
async function preparePhoto(file: File): Promise<File> {
	if (typeof createImageBitmap !== 'function') return file;
	try {
		const bitmap = await createImageBitmap(file);
		try {
			const scale = Math.min(1, maxPixelSide / Math.max(bitmap.width, bitmap.height));
			if (scale === 1) return file;
			const canvas = document.createElement('canvas');
			canvas.width = Math.max(1, Math.round(bitmap.width * scale));
			canvas.height = Math.max(1, Math.round(bitmap.height * scale));
			const context = canvas.getContext('2d');
			if (!context) return file;
			context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
			for (const quality of [0.9, 0.75, 0.6]) {
				const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
				if (blob && blob.size > 0 && blob.size <= maxBytes) {
					return new File([blob], file.name || 'photo.jpeg', { type: 'image/jpeg' });
				}
			}
			return file;
		} finally {
			bitmap.close();
		}
	} catch {
		return file;
	}
}

async function putPhoto(grant: ProofGrant, file: File, signal: AbortSignal): Promise<void> {
	const response = await fetch(grant.uploadUrl, {
		method: 'PUT',
		signal,
		headers: grant.uploadHeaders,
		body: file
	});
	if (!response.ok) throw new Error('Upload response was not successful');
}

export class DeliveryProofController {
	private files: ProofFile[] = [];
	private unattended = false;
	private outcome: ProofOutcome = 'Not Submitted';
	private message = '';
	private busy = false;
	private disposed = false;
	private lifetime = new AbortController();
	private submitted?: Parameters<DeliveryProofAPI['confirmDelivery']>[0];

	constructor(
		private orderId: string,
		private api: DeliveryProofAPI,
		private changed: (snapshot: ProofSnapshot) => void,
		private put: PutPhoto = putPhoto,
		private prepare: PreparePhoto = preparePhoto,
		pending?: Parameters<DeliveryProofAPI['confirmDelivery']>[0]
	) {
		if (pending) {
			this.submitted = { ...pending, proofIds: [...pending.proofIds] };
			this.unattended = pending.unattended;
			this.outcome = 'Pending';
			this.message = 'We’re checking your delivery. Photos and choices can’t be changed while confirmation is pending.';
		}
	}

	snapshot(): ProofSnapshot {
		return {
			files: this.files.map((file) => ({ ...file })),
			unattended: this.unattended,
			outcome: this.outcome,
			message: this.message,
			busy: this.busy,
			locked: this.busy || !!this.submitted || this.outcome === 'Succeeded'
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
		if (this.busy || this.submitted || this.outcome === 'Succeeded') return;
		if (this.files.length + selected.length > 5) {
			this.message = 'Select at most 5 photos.';
			this.publish();
			return;
		}
		for (const file of selected) {
			const valid = types.has(file.type) && file.size > 0 && file.size <= maxBytes;
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

	async removeFile(id: string): Promise<void> {
		if (this.busy || this.submitted || this.outcome === 'Succeeded' || this.disposed) return;
		const row = this.files.find((file) => file.id === id);
		if (!row) return;
		if (!row.uploadRequested) {
			this.files = this.files.filter((file) => file.id !== id);
			this.message = '';
			this.publish();
			return;
		}
		this.busy = true;
		this.outcome = 'Pending';
		row.removalRequested = true;
		row.outcome = 'Pending';
		row.message = 'Removing photo…';
		this.publish();
		try {
			await this.bounded((signal) => this.api.abandonUpload(this.orderId, row.id, signal));
			this.files = this.files.filter((file) => file.id !== id);
			this.outcome = 'Not Submitted';
			this.message = 'Photo removed. You can add a replacement.';
		} catch {
			row.outcome = 'Failed';
			row.message = 'Could not remove this photo. It’s still selected. Try Remove again.';
			this.outcome = 'Failed';
			this.message = row.message;
		} finally {
			this.busy = false;
			this.publish();
		}
	}

	setUnattended(value: boolean): void {
		if (this.busy || this.submitted || this.outcome === 'Succeeded') return;
		this.unattended = value;
		this.publish();
	}

	async upload(): Promise<void> {
		if (this.busy || this.submitted || this.outcome === 'Succeeded' || this.disposed) return;
		this.busy = true;
		this.outcome = 'Pending';
		this.message = 'Uploading photos. Delivery is not yet confirmed.';
		this.publish();
		try {
			for (const row of this.files) {
				if (this.disposed) break;
				if (row.removalRequested || row.outcome === 'Succeeded' || !types.has(row.file.type) || row.file.size < 1 || row.file.size > maxBytes) continue;
				row.outcome = 'Pending';
				row.message = 'Uploading photo…';
				this.publish();
				try {
					row.uploadRequested = true;
					// One prepared copy per photo keeps a retry identical to its first grant.
					row.uploadFile ??= await this.prepare(row.file);
					const file = row.uploadFile;
					const grant = await this.bounded((signal) => this.api.createUpload({
						orderId: this.orderId,
						uploadId: row.id,
						contentType: file.type,
						sizeBytes: file.size
					}, signal));
					row.proofId = grant.proofId;
					// Always inspect after a PUT, including a lost response or a 412
					// from retrying an already stored immutable object.
					try {
						const uploadTimeoutMs = apiTimeoutMs + Math.ceil(file.size / uploadBytesPerSecond) * 1000;
						await this.bounded((signal) => this.put(grant, file, signal), uploadTimeoutMs);
					} catch {
						row.message = 'Checking whether the upload was saved…';
						this.publish();
					}
					await this.bounded((signal) => this.api.verifyUpload(this.orderId, grant.proofId, signal));
					row.outcome = 'Succeeded';
					row.message = '';
				} catch (cause) {
					row.outcome = cause instanceof ProofRejectedError ? 'Rejected' : 'Failed';
					row.message = cause instanceof ProofRejectedError
						? cause.message
						: 'Could not check this upload. Try uploading again or remove the photo.';
				}
				this.publish();
			}
			this.outcome = 'Not Submitted';
			this.message = this.files.length > 0 && this.files.every((row) => row.outcome === 'Succeeded')
				? 'Photos saved. Confirm once the item is delivered.'
				: 'Delivery is not confirmed. Retry or remove unsaved photos.';
		} finally {
			this.busy = false;
			this.publish();
		}
	}

	async confirm(withoutProof = false): Promise<void> {
		if (this.busy || this.outcome === 'Succeeded' || this.disposed) return;
		if (!this.submitted && !withoutProof && (this.files.length === 0 || this.files.some((row) => row.outcome !== 'Succeeded'))) {
			this.outcome = 'Rejected';
			this.message = 'Upload your photos before confirming delivery. Try again or remove any photos that failed.';
			this.publish();
			return;
		}
		this.busy = true;
		this.outcome = 'Pending';
		const effectiveWithoutProof = this.submitted?.withoutProof ?? withoutProof;
		this.message = effectiveWithoutProof
			? 'Checking whether you can confirm without photos…'
			: 'Confirming delivery…';
		this.publish();
		try {
			this.submitted ??= {
				orderId: this.orderId,
				proofIds: withoutProof ? [] : this.files.map((row) => row.proofId!),
				unattended: this.unattended,
				withoutProof
			};
			const submitted = this.submitted;
			await this.bounded((signal) => this.api.confirmDelivery({ ...submitted, proofIds: [...submitted.proofIds] }, signal));
			this.outcome = 'Succeeded';
			this.message = this.submitted.withoutProof
				? 'Delivery confirmed without photos.'
				: 'Delivery confirmed.';
		} catch (cause) {
			if (cause instanceof ProofRejectedError) {
				this.submitted = undefined;
				this.outcome = 'Rejected';
				this.message = cause.message;
			} else {
				this.outcome = 'Failed';
				this.message = 'We couldn’t check your confirmation. Photos and choices can’t be changed. You can safely try again.';
			}
		} finally {
			this.busy = false;
			this.publish();
		}
	}
}
