import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { ProfileService as ProfileServiceDefinition } from '$lib/gen/user/v1/profile_pb';
import { ConnectProfileApi } from './connectProfileApi.ts';

describe('ConnectProfileApi', () => {
	it('maps the profile response and omits blank optional contacts on update', async () => {
		const profile = {
			id: 'profile-id', email: 'casey@example.com', displayName: 'Casey', description: 'Hello',
			telegramHandle: undefined, phoneNumber: undefined, role: UserRole.USER
		};
		const client = {
			getMyProfile: vi.fn(async () => ({ profile })),
			updateMyProfile: vi.fn(async () => ({ profile }))
		} as unknown as Client<typeof ProfileServiceDefinition>;
		const api = new ConnectProfileApi(client);

		expect(await api.getMyProfile()).toEqual(profile);
		expect(await api.updateMyProfile({
			displayName: ' Casey ', description: 'Hello', telegramHandle: ' ', phoneNumber: ''
		})).toEqual(profile);
		expect(client.updateMyProfile).toHaveBeenCalledWith({
			displayName: 'Casey', description: 'Hello', telegramHandle: undefined, phoneNumber: undefined
		});
	});
});
