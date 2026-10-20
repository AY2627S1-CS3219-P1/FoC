export type ProofOutcome = 'Not Submitted' | 'Pending' | 'Succeeded' | 'Rejected' | 'Failed';
export type ProofFile = {
	id: string;
	file: File;
	proofId?: string;
	outcome: ProofOutcome;
	message: string;
};
// The adapter marks only authoritative server rejections as Rejected.
// Transport failures and ambiguous responses remain Failed/unknown.
export class ProofRejectedError extends Error {}

export type ProofGrant = {
	proofId: string;
	uploadUrl: string;
	uploadHeaders: Record<string, string>;
};

// Owned by this feature. The shared Order service owner supplies the adapter
// after the RPC and exporter commit handoff. No inferred shared-service imports.
export interface DeliveryProofAPI {
	createUpload(input: {
		orderId: string;
		uploadId: string;
		contentType: string;
		sizeBytes: number;
	}, signal: AbortSignal): Promise<ProofGrant>;
	verifyUpload(orderId: string, proofId: string, signal: AbortSignal): Promise<void>;
	confirmDelivery(input: {
		orderId: string;
		proofIds: string[];
		unattended: boolean;
		withoutProof: boolean;
	}, signal: AbortSignal): Promise<void>;
}

export type ProofSnapshot = {
	files: ProofFile[];
	unattended: boolean;
	outcome: ProofOutcome;
	message: string;
	locked: boolean;
};

const types = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/heic', 'image/heif']);
const maxBytes = 10_000_000;

type PutPhoto = (grant: ProofGrant, file: File, signal: AbortSignal) => Promise<void>;

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
		private put: PutPhoto = putPhoto
	) {}

	snapshot(): ProofSnapshot {
		return {
			files: this.files.map((file) => ({ ...file })),
			unattended: this.unattended,
			outcome: this.outcome,
			message: this.message,
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

	private async bounded<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
		const signal = AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15_000)]);
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
			this.message = 'Select at most 5 proof photos. Remove a photo before adding another.';
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
					: 'Use a JPEG, PNG, WebP, HEIC or HEIF photo from 1 to 10,000,000 bytes. Remove this file and select a supported photo.'
			});
		}
		this.publish();
	}

	removeFile(id: string): void {
		if (this.busy || this.submitted || this.outcome === 'Succeeded') return;
		this.files = this.files.filter((file) => file.id !== id);
		this.message = '';
		this.publish();
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
		this.message = 'Uploading and checking proof photos. Delivery is not yet confirmed.';
		this.publish();
		try {
			for (const row of this.files) {
				if (this.disposed) break;
				if (row.outcome === 'Succeeded' || !types.has(row.file.type) || row.file.size < 1 || row.file.size > maxBytes) continue;
				row.outcome = 'Pending';
				row.message = 'Uploading photo…';
				this.publish();
				try {
					const grant = await this.bounded((signal) => this.api.createUpload({
						orderId: this.orderId,
						uploadId: row.id,
						contentType: row.file.type,
						sizeBytes: row.file.size
					}, signal));
					row.proofId = grant.proofId;
					// Always inspect after a PUT, including a lost response or a 412
					// from retrying an already stored immutable object.
					try {
						await this.bounded((signal) => this.put(grant, row.file, signal));
					} catch {
						row.message = 'Upload result is unknown. Checking whether the photo was saved…';
						this.publish();
					}
					await this.bounded((signal) => this.api.verifyUpload(this.orderId, grant.proofId, signal));
					row.outcome = 'Succeeded';
					row.message = 'Photo saved and checked.';
				} catch (cause) {
					row.outcome = cause instanceof ProofRejectedError ? 'Rejected' : 'Failed';
					row.message = cause instanceof ProofRejectedError
						? cause.message
						: 'Photo upload could not be confirmed. Keep this photo and try uploading again, or remove it.';
				}
				this.publish();
			}
			this.outcome = 'Not Submitted';
			this.message = this.files.length > 0 && this.files.every((row) => row.outcome === 'Succeeded')
				? 'Photos saved. Confirm delivery when the item has been delivered.'
				: 'Delivery is not confirmed. Retry or remove each photo that was not saved.';
		} finally {
			this.busy = false;
			this.publish();
		}
	}

	async confirm(withoutProof = false): Promise<void> {
		if (this.busy || this.outcome === 'Succeeded' || this.disposed) return;
		if (!this.submitted && !withoutProof && (this.files.length === 0 || this.files.some((row) => row.outcome !== 'Succeeded'))) {
			this.outcome = 'Rejected';
			this.message = 'Upload and check at least one photo. Retry or remove each failed photo before confirming.';
			this.publish();
			return;
		}
		this.busy = true;
		this.outcome = 'Pending';
		const effectiveWithoutProof = this.submitted?.withoutProof ?? withoutProof;
		this.message = effectiveWithoutProof
			? 'Asking the server to confirm an upload outage before confirming delivery…'
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
				? 'Delivery confirmed. The server recorded the upload outage and the missing proof.'
				: 'Delivery confirmed.';
		} catch (cause) {
			if (cause instanceof ProofRejectedError) {
				this.submitted = undefined;
				this.outcome = 'Rejected';
				this.message = cause.message;
			} else {
				this.outcome = 'Failed';
				this.message = 'Delivery confirmation could not be confirmed. Your submitted photos and selection are retained and locked. Try confirmation again; the server will recheck the order and will not duplicate an accepted confirmation.';
			}
		} finally {
			this.busy = false;
			this.publish();
		}
	}
}
