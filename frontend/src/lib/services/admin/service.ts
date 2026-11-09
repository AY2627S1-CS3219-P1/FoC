import type { UserRole } from '$lib/gen/user/v1/auth_pb';
import type { UserProfile } from '../profile/profileApi.ts';
import type { AdminApi, AdminUserStats, AdminUserSummary } from './adminApi.ts';

export class AdminService {
	readonly #api: AdminApi;

	constructor(api: AdminApi) {
		this.#api = api;
	}

	getUserByEmail(email: string): Promise<AdminUserSummary> {
		return this.#api.getUserByEmail(email);
	}

	changeUserRole(userId: string, toRole: UserRole, reason: string): Promise<AdminUserSummary> {
		return this.#api.changeUserRole(userId, toRole, reason);
	}

	getUser(userId: string): Promise<UserProfile> {
		return this.#api.getUser(userId);
	}

	getUserStats(): Promise<AdminUserStats> {
		return this.#api.getUserStats();
	}
}
