import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export const profilePictureApiTimeoutMs = 15_000;

/** An authoritative profile-picture rejection from the backend. */
export class ProfilePictureRejectedError extends Error {
	readonly reason?: string;
	readonly pictureIds: string[];

	constructor(message: string, options?: { reason?: string; pictureIds?: string[] }) {
		super(message);
		this.reason = options?.reason;
		this.pictureIds = options?.pictureIds ?? [];
	}
}

/** The pending upload can no longer be applied; a fresh grant is required. */
export class ProfilePictureNotReadyError extends ProfilePictureRejectedError {}

/** A temporary storage outage; the same operation may be retried. */
export class ProfilePictureTemporaryError extends Error {}

export interface UserProfile {
	id: string;
	email: string;
	displayName: string;
	description: string;
	/** E.164. Absent only for the bootstrapped admin until they add one. */
	phoneNumber?: string;
	role: UserRole;
	/** Stable application reference for the current picture. */
	profilePictureId?: string;
	/** Short-lived pre-signed GET URL; absent when no picture is set. */
	profilePictureUrl?: string;
	/** Epoch milliseconds for the signed URL above. */
	profilePictureExpiresAt?: number;
}

export interface UpdateProfileInput {
	displayName: string;
	description: string;
	phoneNumber: string;
}

export type ProfilePictureGrant = {
	pictureId: string;
	uploadUrl: string;
	uploadHeaders: Record<string, string>;
};

export interface ProfileApi {
	getMyProfile(): Promise<UserProfile>;
	updateMyProfile(input: UpdateProfileInput): Promise<UserProfile>;
	createProfilePictureUpload(
		input: { contentType: string; sizeBytes: number },
		signal: AbortSignal
	): Promise<ProfilePictureGrant>;
	updateMyProfilePicture(pictureId: string, signal: AbortSignal): Promise<UserProfile>;
	removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile>;
}
