import { describe, expect, it, vi } from 'vitest';
import { ProfileService } from './service.svelte';
import type { ProfileApi, UserProfile } from './profileApi';
import { UserRole } from '$lib/gen/user/v1/auth_pb';

const profile = (id: string, url = 'signed-url'): UserProfile => ({
	id, email: `${id}@example.com`, displayName: id, description: '',
	role: UserRole.USER, profilePictureId: 'picture', profilePictureUrl: url
});

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}

function setup() {
	const api: ProfileApi = {
		getMyProfile: vi.fn(async () => profile('A')),
		updateMyProfile: vi.fn(async () => profile('A')),
		createProfilePictureUpload: vi.fn(),
		updateMyProfilePicture: vi.fn(async () => profile('A')),
		removeMyProfilePicture: vi.fn(async () => profile('A'))
	};
	const identity = { updateDisplayName: vi.fn() };
	return { api, identity, service: new ProfileService(api, identity) };
}

describe('profile lifecycle', () => {
	it('deduplicates concurrent reads but fetches a fresh signed URL on the next read', async () => {
		const { api, service } = setup();
		vi.mocked(api.getMyProfile)
			.mockResolvedValueOnce(profile('A', 'first-url'))
			.mockResolvedValueOnce(profile('A', 'fresh-url'));
		const first = service.getMyProfile();
		expect(service.getMyProfile()).toBe(first);
		await first;
		expect((await service.getMyProfile()).profilePictureUrl).toBe('fresh-url');
		expect(api.getMyProfile).toHaveBeenCalledTimes(2);
	});

	it('rejects a previous identity response and preserves the new identity load', async () => {
		const { api, service } = setup();
		const old = deferred<UserProfile>();
		const current = deferred<UserProfile>();
		vi.mocked(api.getMyProfile).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
		const stale = service.getMyProfile();
		const rejected = expect(stale).rejects.toMatchObject({ name: 'AbortError' });
		service.clear();
		expect(service.profile).toBeNull();
		const fresh = service.getMyProfile();
		old.resolve(profile('A'));
		await rejected;
		expect(service.getMyProfile()).toBe(fresh);
		current.resolve(profile('B'));
		await fresh;
		expect(service.profile?.id).toBe('B');
	});

	it('does not publish a picture mutation after logout', async () => {
		const { api, service } = setup();
		const pending = deferred<UserProfile>();
		vi.mocked(api.updateMyProfilePicture).mockReturnValueOnce(pending.promise);
		const saving = service.updateMyProfilePicture('picture', new AbortController().signal);
		const rejected = expect(saving).rejects.toMatchObject({ name: 'AbortError' });
		service.clear();
		pending.resolve(profile('A'));
		await rejected;
		expect(service.profile).toBeNull();
	});

	it('does not let an older read restore a removed picture', async () => {
		const { api, service } = setup();
		await service.getMyProfile();
		const pending = deferred<UserProfile>();
		vi.mocked(api.getMyProfile).mockReturnValueOnce(pending.promise);
		const reading = service.getMyProfile();
		vi.mocked(api.removeMyProfilePicture).mockResolvedValueOnce({
			...profile('A'), profilePictureId: undefined, profilePictureUrl: undefined
		});
		await service.removeMyProfilePicture(new AbortController().signal);
		pending.resolve(profile('A'));
		expect((await reading).profilePictureId).toBeUndefined();
		expect(service.profile?.profilePictureUrl).toBeUndefined();
	});
});
