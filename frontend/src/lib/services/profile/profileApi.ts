import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface UserProfile {
	id: string;
	email: string;
	displayName: string;
	description: string;
	/** E.164. Absent only for the bootstrapped admin until they add one. */
	phoneNumber?: string;
	role: UserRole;
}

export interface UpdateProfileInput {
	displayName: string;
	description: string;
	phoneNumber: string;
}

export interface ProfileApi {
	getMyProfile(): Promise<UserProfile>;
	updateMyProfile(input: UpdateProfileInput): Promise<UserProfile>;
}
