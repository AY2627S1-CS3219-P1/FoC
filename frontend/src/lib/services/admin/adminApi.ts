import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface AdminUserSummary {
	id: string;
	email: string;
	displayName: string;
	role: UserRole;
}

export interface AdminApi {
	getUserByEmail(email: string): Promise<AdminUserSummary>;
	changeUserRole(userId: string, toRole: UserRole, reason: string): Promise<AdminUserSummary>;
}
