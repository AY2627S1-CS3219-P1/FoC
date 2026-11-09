import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { Code, ConnectError } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { ErrorDetailSchema } from '$lib/gen/common/v1/error_pb';
import { ProfileService as ProfileServiceDefinition } from '$lib/gen/user/v1/profile_pb';
import {
	ProfilePictureNotReadyError,
	ProfilePictureRejectedError,
	ProfilePictureTemporaryError
} from './profileApi.ts';
import { ConnectProfileApi } from './connectProfileApi.ts';

function errorWithDetail(reason: string, message: string, pictureIds: string[] = []): ConnectError {
	const detail = {
		reason,
		message,
		fieldViolations: pictureIds.map((id) => ({
			field: 'picture_id',
			description: `${id}: ${message}`
		}))
	};
	return new ConnectError(message, Code.Internal, undefined, [
		{ desc: ErrorDetailSchema, value: detail }
	]);
}

const profile = {
	id: 'profile-id',
	email: 'casey@example.com',
	displayName: 'Casey',
	description: 'Hello',
	phoneNumber: '+6591234567',
	role: UserRole.USER,
	profilePictureId: 'picture-1',
	profilePictureUrl: 'https://storage.invalid/get',
	profilePictureExpiresAt: { seconds: 1700003600n, nanos: 0 }
};

function scenario() {
	const client = {
		getMyProfile: vi.fn(async () => ({ profile })),
		updateMyProfile: vi.fn(async () => ({ profile })),
		createProfilePictureUpload: vi.fn(async () => ({
			pictureId: 'picture-1',
			uploadUrl: 'https://storage.invalid/put',
			uploadHeaders: { 'If-None-Match': '*' },
			expiresAt: { seconds: 1700000000n, nanos: 0 }
		})),
		updateMyProfilePicture: vi.fn(async () => ({ profile })),
		removeMyProfilePicture: vi.fn(async () => ({
			profile: { ...profile, profilePictureUrl: '', profilePictureExpiresAt: undefined }
		}))
	} as unknown as Client<typeof ProfileServiceDefinition>;
	return { client, api: new ConnectProfileApi(client) };
}

describe('ConnectProfileApi', () => {
	it('maps picture grants and expiry timestamps', async () => {
		const { client, api } = scenario();
		const loaded = await api.getMyProfile();
		expect(loaded.profilePictureId).toBe('picture-1');
		expect(loaded.profilePictureUrl).toBe('https://storage.invalid/get');
		expect(loaded.profilePictureExpiresAt).toBe(1700003600000);
		expect(await api.updateMyProfile({
			displayName: ' Casey ', description: 'Hello', phoneNumber: ' +6591234567 '
		})).toEqual({ ...profile, profilePictureExpiresAt: 1700003600000 });
		expect(client.updateMyProfile).toHaveBeenCalledWith({
			displayName: 'Casey', description: 'Hello', phoneNumber: '+6591234567'
		});
	});

	it('requests a grant with a BigInt size and forwards the storage headers', async () => {
		const { client, api } = scenario();
		const grant = await api.createProfilePictureUpload(
			{ contentType: 'image/png', sizeBytes: 1234 },
			new AbortController().signal
		);
		expect(grant).toEqual({
			pictureId: 'picture-1',
			uploadUrl: 'https://storage.invalid/put',
			uploadHeaders: { 'If-None-Match': '*' },
		});
		expect(client.createProfilePictureUpload).toHaveBeenCalledWith(
			{ contentType: 'image/png', sizeBytes: 1234n },
			expect.anything()
		);
	});

	it('applies and removes a picture through the picture id', async () => {
		const { client, api } = scenario();
		expect(await api.updateMyProfilePicture('picture-1', new AbortController().signal)).toMatchObject({
			profilePictureUrl: 'https://storage.invalid/get'
		});
		expect(client.updateMyProfilePicture).toHaveBeenCalledWith(
			{ pictureId: 'picture-1' },
			expect.anything()
		);
		const removed = await api.removeMyProfilePicture(new AbortController().signal);
		expect(removed.profilePictureUrl).toBeUndefined();
		expect(client.removeMyProfilePicture).toHaveBeenCalledWith({}, expect.anything());
	});

	it('does not hand out a grant when the response is incomplete', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createProfilePictureUpload).mockResolvedValueOnce({
			pictureId: '', uploadUrl: ''
		} as never);
		await expect(
			api.createProfilePictureUpload(
				{ contentType: 'image/png', sizeBytes: 1 },
				new AbortController().signal
			)
		).rejects.toThrow('unknown');
	});

	it('maps contract reasons to retryable and rejected errors', async () => {
		const { client, api } = scenario();
		vi.mocked(client.createProfilePictureUpload).mockRejectedValueOnce(
			errorWithDetail(
				'PROFILE_PICTURE_UPLOAD_TEMPORARILY_UNAVAILABLE',
				'Profile picture uploads are temporarily unavailable.'
			)
		);
		await expect(
			api.createProfilePictureUpload(
				{ contentType: 'image/png', sizeBytes: 1 },
				new AbortController().signal
			)
		).rejects.toBeInstanceOf(ProfilePictureTemporaryError);

		const pictureId = '0b8a4b5e-6c1d-4a2b-9e3f-1a2b3c4d5e6f';
		vi.mocked(client.updateMyProfilePicture).mockRejectedValueOnce(
			errorWithDetail('PROFILE_PICTURE_NOT_READY', 'Picture upload is missing.', [pictureId])
		);
		const notReady = await api
			.updateMyProfilePicture('picture-1', new AbortController().signal)
			.catch((cause: unknown) => cause);
		expect(notReady).toBeInstanceOf(ProfilePictureNotReadyError);
		expect((notReady as ProfilePictureNotReadyError).pictureIds).toEqual([pictureId]);

		vi.mocked(client.createProfilePictureUpload).mockRejectedValueOnce(
			errorWithDetail('PROFILE_PICTURE_UPLOAD_UNAVAILABLE', 'Uploads are unavailable.')
		);
		await expect(
			api.createProfilePictureUpload(
				{ contentType: 'image/png', sizeBytes: 1 },
				new AbortController().signal
			)
		).rejects.toBeInstanceOf(ProfilePictureRejectedError);
	});

	it('preserves Connect errors', async () => {
		const { client, api } = scenario();
		vi.mocked(client.updateMyProfilePicture).mockRejectedValueOnce(
			new ConnectError('rejected', Code.FailedPrecondition)
		);
		await expect(
			api.updateMyProfilePicture('picture-1', new AbortController().signal)
		).rejects.toBeInstanceOf(ConnectError);
	});
});
