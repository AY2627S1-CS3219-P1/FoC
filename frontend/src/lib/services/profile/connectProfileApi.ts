import type { Client } from '@connectrpc/connect';
import { ConnectError } from '@connectrpc/connect';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import {
	ProfileService as ProfileServiceDefinition,
	type Profile as ProtoProfile
} from '$lib/gen/user/v1/profile_pb';
import { parseRpcError } from '$lib/services/rpc/errors';
import {
	ProfilePictureNotReadyError,
	ProfilePictureRejectedError,
	ProfilePictureTemporaryError,
	profilePictureApiTimeoutMs,
	type ProfileApi,
	type ProfilePictureGrant,
	type UpdateProfileInput,
	type UserProfile
} from './profileApi.ts';

function rethrow(cause: unknown): never {
	if (cause instanceof ConnectError) {
		const parsed = parseRpcError(cause);
		if (parsed?.reason === 'PROFILE_PICTURE_UPLOAD_TEMPORARILY_UNAVAILABLE') {
			throw new ProfilePictureTemporaryError(parsed.message);
		}
		if (parsed?.reason === 'PROFILE_PICTURE_NOT_READY') {
			throw new ProfilePictureNotReadyError(parsed.message, {
				reason: parsed.reason,
				pictureIds: parsed.photoIds
			});
		}
		if (
			parsed?.reason === 'INVALID_PROFILE_PICTURE' ||
			parsed?.reason === 'PROFILE_PICTURE_UPLOAD_UNAVAILABLE'
		) {
			throw new ProfilePictureRejectedError(parsed.message, { reason: parsed.reason });
		}
	}
	throw cause;
}

type ProfileClient = Client<typeof ProfileServiceDefinition>;

function toProfile(profile: ProtoProfile | undefined): UserProfile {
	if (!profile) throw new Error('Profile response did not include a profile');
	const profilePictureUrl = profile.profilePictureUrl || undefined;
	return {
		id: profile.id,
		email: profile.email,
		displayName: profile.displayName,
		description: profile.description,
		phoneNumber: profile.phoneNumber,
		role: profile.role,
		profilePictureId: profile.profilePictureId || undefined,
		profilePictureUrl,
		profilePictureExpiresAt:
			profilePictureUrl && profile.profilePictureExpiresAt
				? timestampDate(profile.profilePictureExpiresAt).getTime()
				: undefined
	};
}

export class ConnectProfileApi implements ProfileApi {
	readonly #client: ProfileClient;

	constructor(client: ProfileClient) {
		this.#client = client;
	}

	async getMyProfile(): Promise<UserProfile> {
		const response = await this.#client.getMyProfile({});
		return toProfile(response.profile);
	}

	async updateMyProfile(input: UpdateProfileInput): Promise<UserProfile> {
		const response = await this.#client.updateMyProfile({
			displayName: input.displayName.trim(),
			description: input.description,
			phoneNumber: input.phoneNumber.trim()
		});
		return toProfile(response.profile);
	}

	async createProfilePictureUpload(
		input: { contentType: string; sizeBytes: number },
		signal: AbortSignal
	): Promise<ProfilePictureGrant> {
		try {
			const response = await this.#client.createProfilePictureUpload(
				{ contentType: input.contentType, sizeBytes: BigInt(input.sizeBytes) },
				{ signal, timeoutMs: profilePictureApiTimeoutMs }
			);
			if (!response.pictureId || !response.uploadUrl) {
				throw new Error('Profile picture upload grant result is unknown');
			}
			return {
				pictureId: response.pictureId,
				uploadUrl: response.uploadUrl,
				uploadHeaders: response.uploadHeaders
			};
		} catch (cause) {
			rethrow(cause);
		}
	}

	async updateMyProfilePicture(pictureId: string, signal: AbortSignal): Promise<UserProfile> {
		try {
			const response = await this.#client.updateMyProfilePicture(
				{ pictureId },
				{ signal, timeoutMs: profilePictureApiTimeoutMs }
			);
			return toProfile(response.profile);
		} catch (cause) {
			rethrow(cause);
		}
	}

	async removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile> {
		try {
			const response = await this.#client.removeMyProfilePicture(
				{},
				{ signal, timeoutMs: profilePictureApiTimeoutMs }
			);
			return toProfile(response.profile);
		} catch (cause) {
			rethrow(cause);
		}
	}
}
