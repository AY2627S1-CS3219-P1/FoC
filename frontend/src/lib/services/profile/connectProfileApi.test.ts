import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { create } from '@bufbuild/protobuf';
import { TimestampSchema } from '@bufbuild/protobuf/wkt';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { ProfileService as ProfileServiceDefinition } from '$lib/gen/user/v1/profile_pb';
import { ConnectProfileApi } from './connectProfileApi.ts';

const requestedAt = create(TimestampSchema, { seconds: 1_770_000_000n });
const lastCheckedAt = create(TimestampSchema, { seconds: 1_770_000_600n });

describe('ConnectProfileApi', () => {
	it('maps the profile response and trims the update', async () => {
		const profile = {
			id: 'profile-id', email: 'casey@example.com', displayName: 'Casey', description: 'Hello',
			phoneNumber: '+6591234567', role: UserRole.USER
		};
		const client = {
			getMyProfile: vi.fn(async () => ({ profile })),
			updateMyProfile: vi.fn(async () => ({ profile }))
		} as unknown as Client<typeof ProfileServiceDefinition>;
		const api = new ConnectProfileApi(client);

		expect(await api.getMyProfile()).toEqual(profile);
		expect(await api.updateMyProfile({
			displayName: ' Casey ', description: 'Hello', phoneNumber: ' +6591234567 '
		})).toEqual(profile);
		expect(client.updateMyProfile).toHaveBeenCalledWith({
			displayName: 'Casey', description: 'Hello', phoneNumber: '+6591234567'
		});
	});

	it('maps the deletion status timestamps and copies the waiting reasons', async () => {
		const waitingReasons = ['order_outstanding'];
		const client = {
			getMyAccountDeletionStatus: vi.fn(async () => ({
				isDeleting: true,
				requestedAt,
				lastCheckedAt,
				waitingReasons
			}))
		} as unknown as Client<typeof ProfileServiceDefinition>;
		const api = new ConnectProfileApi(client);

		const status = await api.getMyAccountDeletionStatus();

		expect(status.isDeleting).toBe(true);
		expect(status.requestedAt).toEqual(new Date(1_770_000_000_000));
		expect(status.lastCheckedAt).toEqual(new Date(1_770_000_600_000));
		expect(status.waitingReasons).toEqual(['order_outstanding']);
		// A mutation of the returned list must not reach into the response.
		status.waitingReasons.push('mutated');
		expect(waitingReasons).toEqual(['order_outstanding']);
	});

	it('omits absent deletion-status timestamps', async () => {
		const client = {
			getMyAccountDeletionStatus: vi.fn(async () => ({
				isDeleting: false, waitingReasons: []
			}))
		} as unknown as Client<typeof ProfileServiceDefinition>;
		const api = new ConnectProfileApi(client);

		const status = await api.getMyAccountDeletionStatus();

		expect(status.isDeleting).toBe(false);
		expect(status.requestedAt).toBeUndefined();
		expect(status.lastCheckedAt).toBeUndefined();
		expect(status.waitingReasons).toEqual([]);
	});
});
