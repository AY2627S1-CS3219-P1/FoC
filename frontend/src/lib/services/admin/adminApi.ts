import type { UserRole } from '$lib/gen/user/v1/auth_pb';
import type { UserProfile } from '../profile/profileApi.ts';

export interface AdminUserSummary {
	id: string;
	email: string;
	displayName: string;
	role: UserRole;
}

/** Counts exclude deleted users; active users include administrators. */
export interface AdminUserStats {
	activeCount: bigint;
	suspendedCount: bigint;
}

export interface AdminApi {
	getUserByEmail(email: string): Promise<AdminUserSummary>;
	changeUserRole(userId: string, toRole: UserRole, reason: string): Promise<AdminUserSummary>;
	getUser(userId: string): Promise<UserProfile>;
	getUserStats(): Promise<AdminUserStats>;
}
