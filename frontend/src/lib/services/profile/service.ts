import type { ProfileApi, UpdateProfileInput, UserProfile } from './profileApi.ts';
export interface ProfileIdentity {
	updateDisplayName(displayName: string): void;
}

export class ProfileService {
	readonly #api: ProfileApi;
	readonly #auth: ProfileIdentity;

	constructor(api: ProfileApi, auth: ProfileIdentity) {
		this.#api = api;
		this.#auth = auth;
	}

	getMyProfile(): Promise<UserProfile> {
		return this.#api.getMyProfile();
	}

	async updateMyProfile(input: UpdateProfileInput): Promise<UserProfile> {
		const profile = await this.#api.updateMyProfile(input);
		this.#auth.updateDisplayName(profile.displayName);
		return profile;
	}
}
