import { describe, expect, it, vi } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import type { ProfilePictureGrant, UserProfile } from '$lib/services/profile/profileApi';
import {
	ProfilePictureController,
	type ProfilePictureApi
} from './controller';

const file = (name = 'picture.png', type = 'image/png') => new File(['photo'], name, { type });

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
		uploadHeaders: { 'If-None-Match': '*' },
		expiresAt: 0
	};
}

function scenario(
	putImpl: (grant: ProfilePictureGrant, file: File, signal: AbortSignal) => Promise<void> = async () => {}
) {
	const api: ProfilePictureApi = {
		createProfilePictureUpload: vi.fn(async () => grant()),
		updateMyProfilePicture: vi.fn(async () => profile({ profilePictureUrl: 'https://picture' })),
		removeMyProfilePicture: vi.fn(async () => profile())
	};
	const put = vi.fn(putImpl);
	const controller = new ProfilePictureController(api, () => {}, put);
	return { api, put, controller };
}

describe('profile picture controller', () => {
	it('grants, PUTs and applies one picture in order', async () => {
		const { api, put, controller } = scenario();
		const updated = await controller.upload(file());
		expect(updated?.profilePictureUrl).toBe('https://picture');
		expect(api.createProfilePictureUpload).toHaveBeenCalledWith(
			{ contentType: 'image/png', sizeBytes: 5 },
			expect.anything()
		);
		expect(put).toHaveBeenCalledWith(grant(), expect.any(File), expect.anything());
		expect(api.updateMyProfilePicture).toHaveBeenCalledWith('upload-1', expect.anything());
		expect(controller.snapshot()).toMatchObject({ outcome: 'Succeeded', busy: false });
	});

	it('rejects unsupported or oversized files before calling the API', async () => {
		const { api, put, controller } = scenario();
		expect(await controller.upload(file('notes.txt', 'text/plain'))).toBeNull();
		expect(await controller.upload(new File([new Uint8Array(10_000_001)], 'big.png', { type: 'image/png' }))).toBeNull();
		expect(api.createProfilePictureUpload).not.toHaveBeenCalled();
		expect(put).not.toHaveBeenCalled();
		expect(controller.snapshot()).toMatchObject({ outcome: 'Failed', busy: false });
	});

	it('stops before the update when the PUT fails, and can retry with a fresh grant', async () => {
		const { api, put, controller } = scenario();
		put.mockRejectedValueOnce(new Error('storage down'));
		expect(await controller.upload(file())).toBeNull();
		expect(api.updateMyProfilePicture).not.toHaveBeenCalled();
		expect(controller.snapshot().outcome).toBe('Failed');

		expect(await controller.upload(file('retry.png'))).not.toBeNull();
		expect(api.createProfilePictureUpload).toHaveBeenCalledTimes(2);
		expect(api.updateMyProfilePicture).toHaveBeenCalledOnce();
	});

	it('retries an ambiguous update once with the same upload id', async () => {
		const { api, controller } = scenario();
		vi.mocked(api.updateMyProfilePicture)
			.mockRejectedValueOnce(new ConnectError('lost', Code.Unavailable))
			.mockResolvedValueOnce(profile({ profilePictureUrl: 'https://picture' }));
		const updated = await controller.upload(file());
		expect(updated?.profilePictureUrl).toBe('https://picture');
		expect(api.updateMyProfilePicture).toHaveBeenCalledTimes(2);
		expect(api.updateMyProfilePicture).toHaveBeenNthCalledWith(1, 'upload-1', expect.anything());
		expect(api.updateMyProfilePicture).toHaveBeenNthCalledWith(2, 'upload-1', expect.anything());
	});

	it('removes the picture and reports rejection', async () => {
		const { api, controller } = scenario();
		expect(await controller.remove()).not.toBeNull();
		expect(api.removeMyProfilePicture).toHaveBeenCalledOnce();
		expect(controller.snapshot().outcome).toBe('Succeeded');

		vi.mocked(api.removeMyProfilePicture).mockRejectedValueOnce(
			new ConnectError('denied', Code.PermissionDenied)
		);
		expect(await controller.remove()).toBeNull();
		expect(controller.snapshot().message).toContain('cannot change');
	});

	it('aborts an in-flight upload on dispose', async () => {
		let started!: () => void;
		const uploading = new Promise<void>((resolve) => {
			started = resolve;
		});
		const { api, controller } = scenario((_grant, _file, signal) => {
			started();
			return new Promise<void>((_resolve, reject) => {
				signal.addEventListener('abort', () => reject(signal.reason), { once: true });
			});
		});
		const work = controller.upload(file());
		await uploading;
		controller.dispose();
		expect(await work).toBeNull();
		expect(api.updateMyProfilePicture).not.toHaveBeenCalled();
	});
});
