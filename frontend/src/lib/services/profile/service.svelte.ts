import type {
	ProfileApi,
	ProfilePictureGrant,
	UpdateProfileInput,
	UserProfile
} from './profileApi.ts';

export interface ProfileIdentity {
	updateDisplayName(displayName: string): void;
}

export class ProfileService {
	readonly #api: ProfileApi;
	readonly #auth: ProfileIdentity;

	/** Last loaded profile, shared by the profile page and the sidebar. */
	profile = $state<UserProfile | null>(null);

	constructor(api: ProfileApi, auth: ProfileIdentity) {
		this.#api = api;
		this.#auth = auth;
	}

	async getMyProfile(): Promise<UserProfile> {
		const profile = await this.#api.getMyProfile();
		this.profile = profile;
		return profile;
	}

	/** Loads once for layout-level consumers; the profile page may force a read. */
	async ensureProfile(): Promise<UserProfile> {
		return this.profile ?? (await this.getMyProfile());
	}

	async updateMyProfile(input: UpdateProfileInput): Promise<UserProfile> {
		const profile = await this.#api.updateMyProfile(input);
		this.profile = profile;
		this.#auth.updateDisplayName(profile.displayName);
		return profile;
	}

	createProfilePictureUpload(
		input: { contentType: string; sizeBytes: number },
		signal: AbortSignal
	): Promise<ProfilePictureGrant> {
		return this.#api.createProfilePictureUpload(input, signal);
	}

	async updateMyProfilePicture(uploadId: string, signal: AbortSignal): Promise<UserProfile> {
		const profile = await this.#api.updateMyProfilePicture(uploadId, signal);
		this.profile = profile;
		return profile;
	}

	async removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile> {
		const profile = await this.#api.removeMyProfilePicture(signal);
		this.profile = profile;
		return profile;
	}

	clear(): void {
		this.profile = null;
	}
}
