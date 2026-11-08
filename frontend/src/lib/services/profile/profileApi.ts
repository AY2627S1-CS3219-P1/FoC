import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface UserProfile {
	id: string;
	email: string;
	displayName: string;
	description: string;
	/** E.164. Absent only for the bootstrapped admin until they add one. */
	phoneNumber?: string;
	role: UserRole;
	/** True once the account's irreversible deletion has been accepted. */
	isDeleting: boolean;
}

export interface UpdateProfileInput {
	displayName: string;
	description: string;
	phoneNumber: string;
}

/** The deletion worker's progress for the authenticated account. */
export interface AccountDeletionStatus {
	isDeleting: boolean;
	/** The original, immutable deletion request time. */
	requestedAt?: Date;
	/** When the worker last completed an outstanding-work check. */
	lastCheckedAt?: Date;
	/** Stable reason codes explaining why finalization is waiting. */
	waitingReasons: string[];
}

export interface ProfileApi {
	getMyProfile(): Promise<UserProfile>;
	updateMyProfile(input: UpdateProfileInput): Promise<UserProfile>;
	getMyAccountDeletionStatus(): Promise<AccountDeletionStatus>;
}
