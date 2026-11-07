import { Code, ConnectError } from '@connectrpc/connect';
import type { ProfilePictureGrant, UserProfile } from '$lib/services/profile/profileApi';

export type ProfilePictureOutcome = 'Idle' | 'Uploading' | 'Saving' | 'Succeeded' | 'Failed';

export type ProfilePictureSnapshot = {
	outcome: ProfilePictureOutcome;
	message: string;
	busy: boolean;
};

export type ProfilePictureApi = {
	createProfilePictureUpload(
		input: { contentType: string; sizeBytes: number },
		signal: AbortSignal
	): Promise<ProfilePictureGrant>;
	updateMyProfilePicture(pictureId: string, signal: AbortSignal): Promise<UserProfile>;
	removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile>;
};

type PutPicture = (grant: ProfilePictureGrant, file: File, signal: AbortSignal) => Promise<void>;

const acceptedTypes = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/heic', 'image/heif']);
const extensions: Record<string, string> = {
	jpg: 'image/jpeg',
	jpeg: 'image/jpeg',
	png: 'image/png',
	webp: 'image/webp',
	heic: 'image/heic',
	heif: 'image/heif'
};
const maxBytes = 10_000_000;
const apiTimeoutMs = 15_000;
// Allow 64 KiB/s plus API-sized overhead for a bounded direct upload.
const uploadBytesPerSecond = 64 * 1024;

// Chrome and Firefox leave File.type empty for HEIC/HEIF files; infer the
// type from the extension rather than blocking a file the backend accepts.
function contentTypeFor(file: File): string | undefined {
	if (file.type) return file.type;
	const extension = file.name.split('.').pop()?.toLowerCase();
	return extension ? extensions[extension] : undefined;
}

async function putPicture(grant: ProfilePictureGrant, file: File, signal: AbortSignal): Promise<void> {
	const response = await fetch(grant.uploadUrl, {
		method: 'PUT',
		signal,
		headers: grant.uploadHeaders,
		body: file
	});
	if (!response.ok) throw new Error('Upload response was not successful');
}

function isAmbiguous(cause: unknown): boolean {
	return (
		cause instanceof ConnectError &&
		[Code.Unavailable, Code.DeadlineExceeded, Code.Canceled, Code.Internal, Code.Unknown].includes(
			cause.code
		)
	);
}

function errorMessage(cause: unknown): string {
	if (cause instanceof ConnectError) {
		if (cause.code === Code.PermissionDenied) return 'Your account cannot change its profile picture.';
		if (cause.code === Code.Unauthenticated) return 'Your session has expired. Sign in again.';
		if (cause.code === Code.InvalidArgument || cause.code === Code.FailedPrecondition) {
			return 'That file could not be accepted. Choose a JPEG, PNG, WebP, HEIC or HEIF file up to 10MB.';
		}
	}
	return 'We could not update your profile picture. Check your connection and try again.';
}

/**
 * Uploads one profile picture and immediately applies it. There is no separate
 * verify or confirm step: the update call validates the stored object and
 * updates the profile in one round trip. A lost update response is retried once
 * because the backend treats the same picture ID idempotently.
 */
export class ProfilePictureController {
	#outcome: ProfilePictureOutcome = 'Idle';
	#message = '';
	#busy = false;
	#disposed = false;
	#lifetime = new AbortController();

	constructor(
		private api: ProfilePictureApi,
		private changed: (snapshot: ProfilePictureSnapshot) => void,
		private put: PutPicture = putPicture
	) {}

	snapshot(): ProfilePictureSnapshot {
		return { outcome: this.#outcome, message: this.#message, busy: this.#busy };
	}

	dispose(): void {
		this.#disposed = true;
		this.#lifetime.abort();
	}

	async upload(file: File): Promise<UserProfile | null> {
		if (this.#busy || this.#disposed) return null;
		const contentType = contentTypeFor(file);
		if (!contentType || !acceptedTypes.has(contentType) || file.size < 1 || file.size > maxBytes) {
			this.#outcome = 'Failed';
			this.#message = 'Choose a JPEG, PNG, WebP, HEIC or HEIF file up to 10MB.';
			this.#publish();
			return null;
		}

		this.#busy = true;
		this.#outcome = 'Uploading';
		this.#message = 'Uploading your picture…';
		this.#publish();
		try {
			const grant = await this.#bounded((signal) =>
				this.api.createProfilePictureUpload(
					{ contentType, sizeBytes: file.size },
					signal
				)
			);
			const uploadTimeoutMs = apiTimeoutMs + Math.ceil(file.size / uploadBytesPerSecond) * 1000;
			await this.#bounded((signal) => this.put(grant, file, signal), uploadTimeoutMs);

			this.#outcome = 'Saving';
			this.#message = 'Saving your picture…';
			this.#publish();
			const profile = await this.#apply(grant.pictureId);
			this.#outcome = 'Succeeded';
			this.#message = 'Your profile picture has been updated.';
			return profile;
		} catch (cause) {
			this.#outcome = 'Failed';
			this.#message = errorMessage(cause);
			return null;
		} finally {
			this.#busy = false;
			this.#publish();
		}
	}

	async remove(): Promise<UserProfile | null> {
		if (this.#busy || this.#disposed) return null;
		this.#busy = true;
		this.#outcome = 'Saving';
		this.#message = 'Removing your picture…';
		this.#publish();
		try {
			const profile = await this.#bounded((signal) => this.api.removeMyProfilePicture(signal));
			this.#outcome = 'Succeeded';
			this.#message = 'Your profile picture has been removed.';
			return profile;
		} catch (cause) {
			this.#outcome = 'Failed';
			this.#message = errorMessage(cause);
			return null;
		} finally {
			this.#busy = false;
			this.#publish();
		}
	}

	async #apply(pictureId: string): Promise<UserProfile> {
		try {
			return await this.#bounded((signal) => this.api.updateMyProfilePicture(pictureId, signal));
		} catch (cause) {
			if (!isAmbiguous(cause)) throw cause;
			return this.#bounded((signal) => this.api.updateMyProfilePicture(pictureId, signal));
		}
	}

	async #bounded<T>(
		operation: (signal: AbortSignal) => Promise<T>,
		timeoutMs = apiTimeoutMs
	): Promise<T> {
		const signal = AbortSignal.any([this.#lifetime.signal, AbortSignal.timeout(timeoutMs)]);
		signal.throwIfAborted();
		return new Promise<T>((resolve, reject) => {
			const abort = () => reject(signal.reason);
			signal.addEventListener('abort', abort, { once: true });
			operation(signal)
				.then(resolve, reject)
				.finally(() => signal.removeEventListener('abort', abort));
		});
	}

	#publish(): void {
		if (!this.#disposed) this.changed(this.snapshot());
	}
}
