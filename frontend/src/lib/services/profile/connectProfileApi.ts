import type { Client } from '@connectrpc/connect';
import {
	ProfileService as ProfileServiceDefinition,
	type Profile as ProtoProfile
} from '$lib/gen/user/v1/profile_pb';
import type { ProfileApi, UpdateProfileInput, UserProfile } from './profileApi.ts';

type ProfileClient = Client<typeof ProfileServiceDefinition>;

function toProfile(profile: ProtoProfile | undefined): UserProfile {
	if (!profile) throw new Error('Profile response did not include a profile');
	return {
		id: profile.id,
		email: profile.email,
		displayName: profile.displayName,
		description: profile.description,
		telegramHandle: profile.telegramHandle,
		phoneNumber: profile.phoneNumber,
		role: profile.role
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
			telegramHandle: input.telegramHandle?.trim() || undefined,
			phoneNumber: input.phoneNumber?.trim() || undefined
		});
		return toProfile(response.profile);
	}
}
