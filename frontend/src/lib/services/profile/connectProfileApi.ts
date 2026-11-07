import type { Client } from '@connectrpc/connect';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import {
	ProfileService as ProfileServiceDefinition,
	type Profile as ProtoProfile
} from '$lib/gen/user/v1/profile_pb';
import type {
	ProfileApi,
	ProfilePictureGrant,
	UpdateProfileInput,
	UserProfile
} from './profileApi.ts';

type ProfileClient = Client<typeof ProfileServiceDefinition>;

function toProfile(profile: ProtoProfile | undefined): UserProfile {
	if (!profile) throw new Error('Profile response did not include a profile');
	return {
		id: profile.id,
		email: profile.email,
		displayName: profile.displayName,
		description: profile.description,
		phoneNumber: profile.phoneNumber,
		role: profile.role,
		profilePictureUrl: profile.profilePictureUrl || undefined,
		profilePictureExpiresAt: profile.profilePictureExpiresAt
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
		const response = await this.#client.createProfilePictureUpload(
			{ contentType: input.contentType, sizeBytes: BigInt(input.sizeBytes) },
			{ signal, timeoutMs: 15000 }
		);
		if (!response.uploadId || !response.uploadUrl) {
			throw new Error('Profile picture upload grant result is unknown');
		}
		return {
			uploadId: response.uploadId,
			uploadUrl: response.uploadUrl,
			uploadHeaders: response.uploadHeaders,
			expiresAt: response.expiresAt ? timestampDate(response.expiresAt).getTime() : 0
		};
	}

	async updateMyProfilePicture(uploadId: string, signal: AbortSignal): Promise<UserProfile> {
		const response = await this.#client.updateMyProfilePicture(
			{ uploadId },
			{ signal, timeoutMs: 15000 }
		);
		return toProfile(response.profile);
	}

	async removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile> {
		const response = await this.#client.removeMyProfilePicture({}, { signal, timeoutMs: 15000 });
		return toProfile(response.profile);
	}
}
