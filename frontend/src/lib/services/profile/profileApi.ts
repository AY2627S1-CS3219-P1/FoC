import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface UserProfile {
	id: string;
	email: string;
	displayName: string;
	description: string;
	telegramHandle?: string;
	phoneNumber?: string;
	role: UserRole;
}

export interface UpdateProfileInput {
	displayName: string;
	description: string;
	telegramHandle?: string;
	phoneNumber?: string;
}

export interface ProfileApi {
	getMyProfile(): Promise<UserProfile>;
	updateMyProfile(input: UpdateProfileInput): Promise<UserProfile>;
}
