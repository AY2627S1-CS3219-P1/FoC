import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface UserProfile {
	id: string;
	email: string;
	displayName: string;
	description: string;
	/** E.164. Absent only for the bootstrapped admin until they add one. */
	phoneNumber?: string;
	role: UserRole;
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
	uploadId: string;
	uploadUrl: string;
	uploadHeaders: Record<string, string>;
	expiresAt: number;
};

export interface ProfileApi {
	getMyProfile(): Promise<UserProfile>;
	updateMyProfile(input: UpdateProfileInput): Promise<UserProfile>;
	createProfilePictureUpload(
		input: { contentType: string; sizeBytes: number },
		signal: AbortSignal
	): Promise<ProfilePictureGrant>;
	updateMyProfilePicture(uploadId: string, signal: AbortSignal): Promise<UserProfile>;
	removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile>;
}
