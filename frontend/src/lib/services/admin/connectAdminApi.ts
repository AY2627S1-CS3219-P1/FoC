import type { Client } from '@connectrpc/connect';
import {
	UserAdminService as UserAdminServiceDefinition,
	type UserSummary as ProtoUserSummary
} from '$lib/gen/user/v1/admin_pb';
import type { AdminApi, AdminUserSummary } from './adminApi.ts';
import type { UserRole } from '$lib/gen/user/v1/auth_pb';

type AdminClient = Client<typeof UserAdminServiceDefinition>;

function toSummary(user: ProtoUserSummary | undefined): AdminUserSummary {
	if (!user) throw new Error('Admin response did not include a user');
	return { id: user.id, email: user.email, displayName: user.displayName, role: user.role };
}

export class ConnectAdminApi implements AdminApi {
	readonly #client: AdminClient;

	constructor(client: AdminClient) {
		this.#client = client;
	}

	async getUserByEmail(email: string): Promise<AdminUserSummary> {
		const response = await this.#client.getUserByEmail({ email: email.trim() });
		return toSummary(response.user);
	}

	async changeUserRole(userId: string, toRole: UserRole, reason: string): Promise<AdminUserSummary> {
		const response = await this.#client.changeUserRole({ userId, toRole, reason: reason.trim() });
		return toSummary(response.user);
	}
}
