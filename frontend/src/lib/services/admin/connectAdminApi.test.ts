import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { UserAdminService as UserAdminServiceDefinition } from '$lib/gen/user/v1/admin_pb';
import { ConnectAdminApi } from './connectAdminApi.ts';

describe('ConnectAdminApi', () => {
	it('looks up exact email and sends a role change request', async () => {
		const user = { id: 'user-id', email: 'casey@example.com', displayName: 'Casey', role: UserRole.USER };
		const client = {
			getUserByEmail: vi.fn(async () => ({ user })),
			changeUserRole: vi.fn(async () => ({ user: { ...user, role: UserRole.SUSPENDED_USER } }))
		} as unknown as Client<typeof UserAdminServiceDefinition>;
		const api = new ConnectAdminApi(client);

		expect(await api.getUserByEmail(' casey@example.com ')).toEqual(user);
		expect(client.getUserByEmail).toHaveBeenCalledWith({ email: 'casey@example.com' });
		expect(await api.changeUserRole('user-id', UserRole.SUSPENDED_USER, ' policy violation ')).toEqual({
			...user, role: UserRole.SUSPENDED_USER
		});
		expect(client.changeUserRole).toHaveBeenCalledWith({
			userId: 'user-id', toRole: UserRole.SUSPENDED_USER, reason: 'policy violation'
		});
	});

	it('reads one user profile by ID and the user counts', async () => {
		const profile = {
			id: 'user-id', email: 'casey@example.com', displayName: 'Casey', description: 'Hi',
			phoneNumber: '+6591234567', role: UserRole.USER
		};
		const client = {
			getUser: vi.fn(async () => ({ profile })),
			getUserStats: vi.fn(async () => ({ activeCount: 12n, suspendedCount: 3n }))
		} as unknown as Client<typeof UserAdminServiceDefinition>;
		const api = new ConnectAdminApi(client);

		expect(await api.getUser(' user-id ')).toEqual(profile);
		expect(client.getUser).toHaveBeenCalledWith({ userId: 'user-id' });
		expect(await api.getUserStats()).toEqual({ activeCount: 12n, suspendedCount: 3n });
	});

	it('rejects a user response without a profile', async () => {
		const client = { getUser: vi.fn(async () => ({})) } as unknown as Client<typeof UserAdminServiceDefinition>;
		await expect(new ConnectAdminApi(client).getUser('user-id')).rejects.toThrow();
	});
});
