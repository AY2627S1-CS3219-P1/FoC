import { describe, expect, it, vi } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import {
	ProfilePictureNotReadyError,
	ProfilePictureTemporaryError,
	type ProfilePictureGrant,
	type UserProfile
} from '$lib/services/profile/profileApi';
import {
	PictureUploadFailedError,
	ProfilePictureController,
	putPicture,
	type ProfilePictureApi
} from './controller.ts';

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
		pictureId: 'picture-1',
		uploadUrl: 'https://storage.invalid/put',
		uploadHeaders: { 'If-None-Match': '*' },
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
		expect(api.updateMyProfilePicture).toHaveBeenCalledWith('picture-1', expect.anything());
		expect(controller.snapshot()).toMatchObject({ outcome: 'Succeeded', busy: false });
	});

	it('rejects an empty file before calling the API', async () => {
		const { api, controller } = scenario();
		expect(await controller.upload(new File([], 'empty.png', { type: 'image/png' }))).toBeNull();
		expect(api.createProfilePictureUpload).not.toHaveBeenCalled();
		expect(controller.snapshot().outcome).toBe('Failed');
	});

	it('does not infer a type from a filename with no extension', async () => {
		const { api, controller } = scenario();
		expect(await controller.upload(new File(['photo'], 'heic', { type: '' }))).toBeNull();
		expect(api.createProfilePictureUpload).not.toHaveBeenCalled();
	});

	it('ignores re-entry while busy or after dispose', async () => {
		let started!: () => void;
		const uploading = new Promise<void>((resolve) => {
			started = resolve;
		});
		let release!: () => void;
		const put = new Promise<void>((resolve) => {
			release = resolve;
		});
		const { api, controller } = scenario(() => {
			started();
			return put;
		});
		const first = controller.upload(file());
		await uploading;
		expect(await controller.upload(file('second.png'))).toBeNull();
		expect(api.createProfilePictureUpload).toHaveBeenCalledOnce();
		release();
		await first;

		controller.dispose();
		expect(await controller.upload(file('third.png'))).toBeNull();
		expect(api.createProfilePictureUpload).toHaveBeenCalledOnce();
	});

	it('rejects unsupported or oversized files before calling the API', async () => {
		const { api, put, controller } = scenario();
		expect(await controller.upload(file('notes.txt', 'text/plain'))).toBeNull();
		expect(await controller.upload(new File([new Uint8Array(10_000_001)], 'big.png', { type: 'image/png' }))).toBeNull();
		expect(api.createProfilePictureUpload).not.toHaveBeenCalled();
		expect(put).not.toHaveBeenCalled();
		expect(controller.snapshot()).toMatchObject({ outcome: 'Failed', busy: false });
	});

	it('infers a missing HEIC content type from the extension', async () => {
		const { api, controller } = scenario();
		const heic = new File(['photo'], 'phone.heic', { type: '' });
		expect(await controller.upload(heic)).not.toBeNull();
		expect(api.createProfilePictureUpload).toHaveBeenCalledWith(
			{ contentType: 'image/heic', sizeBytes: 5 },
			expect.anything()
		);

		const opaque = new File(['photo'], 'phone-copy.heic', { type: 'application/octet-stream' });
		expect(await controller.upload(opaque)).not.toBeNull();
		expect(api.createProfilePictureUpload).toHaveBeenLastCalledWith(
			{ contentType: 'image/heic', sizeBytes: 5 },
			expect.anything()
		);
	});

	it('stops before the update when the PUT fails, and can retry with a fresh grant', async () => {
		const { api, put, controller } = scenario();
		put.mockRejectedValueOnce(new PictureUploadFailedError('storage down'));
		expect(await controller.upload(file())).toBeNull();
		expect(api.updateMyProfilePicture).not.toHaveBeenCalled();
		expect(controller.snapshot().outcome).toBe('Failed');
		expect(controller.snapshot().message).toContain('upload that picture');

		expect(await controller.upload(file('retry.png'))).not.toBeNull();
		expect(api.createProfilePictureUpload).toHaveBeenCalledTimes(2);
		expect(api.updateMyProfilePicture).toHaveBeenCalledOnce();
	});

	it('retries an ambiguous update once with the same picture id', async () => {
		const { api, controller } = scenario();
		vi.mocked(api.updateMyProfilePicture)
			.mockRejectedValueOnce(new ConnectError('lost', Code.Unavailable))
			.mockResolvedValueOnce(profile({ profilePictureUrl: 'https://picture' }));
		const updated = await controller.upload(file());
		expect(updated?.profilePictureUrl).toBe('https://picture');
		expect(api.updateMyProfilePicture).toHaveBeenCalledTimes(2);
		expect(api.updateMyProfilePicture).toHaveBeenNthCalledWith(1, 'picture-1', expect.anything());
		expect(api.updateMyProfilePicture).toHaveBeenNthCalledWith(2, 'picture-1', expect.anything());
	});

	it('retries a timeout once with the same picture id', async () => {
		const { api, controller } = scenario();
		vi.mocked(api.updateMyProfilePicture)
			.mockRejectedValueOnce(new DOMException('timed out', 'TimeoutError'))
			.mockResolvedValueOnce(profile({ profilePictureUrl: 'https://picture' }));
		expect(await controller.upload(file())).not.toBeNull();
		expect(api.updateMyProfilePicture).toHaveBeenCalledTimes(2);
		expect(api.updateMyProfilePicture).toHaveBeenNthCalledWith(2, 'picture-1', expect.anything());
	});

	it('retries a temporary storage outage but not a not-ready picture', async () => {
		const temporary = scenario();
		vi.mocked(temporary.api.updateMyProfilePicture)
			.mockRejectedValueOnce(new ProfilePictureTemporaryError('Try again shortly.'))
			.mockResolvedValueOnce(profile({ profilePictureUrl: 'https://picture' }));
		expect(await temporary.controller.upload(file())).not.toBeNull();
		expect(temporary.api.updateMyProfilePicture).toHaveBeenCalledTimes(2);

		const notReady = scenario();
		vi.mocked(notReady.api.updateMyProfilePicture).mockRejectedValueOnce(
			new ProfilePictureNotReadyError('Picture upload is missing; upload it again.')
		);
		expect(await notReady.controller.upload(file())).toBeNull();
		expect(notReady.api.updateMyProfilePicture).toHaveBeenCalledOnce();
		expect(notReady.controller.snapshot().message).toContain('missing');
	});

	it('does not retry a definitive rejection', async () => {
		const { api, controller } = scenario();
		vi.mocked(api.updateMyProfilePicture).mockRejectedValueOnce(
			new ConnectError('rejected', Code.FailedPrecondition)
		);
		expect(await controller.upload(file())).toBeNull();
		expect(api.updateMyProfilePicture).toHaveBeenCalledOnce();
		expect(controller.snapshot().outcome).toBe('Failed');
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


describe('profile picture direct upload', () => {
	it('sends object bytes without cookies or redirects', async () => {
		const picture = file();
		const direct = grant();
		let seen: RequestInit | undefined;
		const send = (async (_url: string | URL | Request, init?: RequestInit) => {
			seen = init;
			return new Response('', { status: 200 });
		}) as typeof fetch;

		await putPicture(direct, picture, new AbortController().signal, send);

		expect(seen).toMatchObject({
			method: 'PUT',
			body: picture,
			headers: direct.uploadHeaders,
			credentials: 'omit',
			redirect: 'error'
		});
	});
});
