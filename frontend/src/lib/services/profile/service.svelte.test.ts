import { describe, expect, it, vi } from 'vitest';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import type { ProfileApi, ProfilePictureGrant, UserProfile } from './profileApi.ts';
import { ProfileService } from './service.svelte.ts';

function profile(overrides: Partial<UserProfile> = {}): UserProfile {
	return {
		id: 'user-1',
		email: 'casey@example.com',
		displayName: 'Casey',
		description: '',
		phoneNumber: '+6591234567',
		role: UserRole.USER,
		...overrides
	};
}

function grant(): ProfilePictureGrant {
	return {
		uploadId: 'upload-1',
		uploadUrl: 'https://storage.invalid/put',
		uploadHeaders: {},
		expiresAt: 0
	};
}

function createApi(): ProfileApi {
	return {
		getMyProfile: vi.fn(async () => profile()),
		updateMyProfile: vi.fn(async () => profile({ displayName: 'Updated' })),
		createProfilePictureUpload: vi.fn(async () => grant()),
		updateMyProfilePicture: vi.fn(async () => profile({ profilePictureUrl: 'https://picture' })),
		removeMyProfilePicture: vi.fn(async () => profile())
	};
}

describe('ProfileService', () => {
	it('loads and caches the profile for layout consumers', async () => {
		const api = createApi();
		const service = new ProfileService(api, { updateDisplayName: vi.fn() });
		expect(service.profile).toBeNull();

		const loaded = await service.getMyProfile();
		expect(loaded.displayName).toBe('Casey');
		expect(service.profile).toEqual(loaded);

		await service.ensureProfile();
		expect(api.getMyProfile).toHaveBeenCalledTimes(1);

		await service.getMyProfile();
		expect(api.getMyProfile).toHaveBeenCalledTimes(2);
	});

	it('updates shared state and the authenticated display name', async () => {
		const api = createApi();
		const auth = { updateDisplayName: vi.fn() };
		const service = new ProfileService(api, auth);

		const updated = await service.updateMyProfile({
			displayName: 'Updated', description: '', phoneNumber: '+6591234567'
		});

		expect(service.profile).toEqual(updated);
		expect(auth.updateDisplayName).toHaveBeenCalledWith('Updated');
	});

	it('updates and removes a picture in shared state', async () => {
		const api = createApi();
		const service = new ProfileService(api, { updateDisplayName: vi.fn() });

		const updated = await service.updateMyProfilePicture('upload-1', new AbortController().signal);
		expect(updated.profilePictureUrl).toBe('https://picture');
		expect(service.profile).toEqual(updated);

		const removed = await service.removeMyProfilePicture(new AbortController().signal);
		expect(removed.profilePictureUrl).toBeUndefined();
		expect(service.profile).toEqual(removed);
	});

	it('keeps the previous profile when a request fails', async () => {
		const api = createApi();
		const service = new ProfileService(api, { updateDisplayName: vi.fn() });
		await service.getMyProfile();
		const previous = service.profile;
		vi.mocked(api.updateMyProfilePicture).mockRejectedValueOnce(new Error('down'));

		await expect(
			service.updateMyProfilePicture('upload-1', new AbortController().signal)
		).rejects.toThrow('down');
		expect(service.profile).toEqual(previous);
	});

	it('clears state on logout or a user change', async () => {
		const service = new ProfileService(createApi(), { updateDisplayName: vi.fn() });
		await service.getMyProfile();
		service.clear();
		expect(service.profile).toBeNull();
	});
});
