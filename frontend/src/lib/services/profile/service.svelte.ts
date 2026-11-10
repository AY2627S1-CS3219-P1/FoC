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

	/** Current display state only; every completed read is fetched again. */
	profile = $state<UserProfile | null>(null);
	#profileLoad: Promise<UserProfile> | undefined;
	#pictureRefresh: Promise<UserProfile | undefined> | undefined;
	#generation = 0;
	#writeRevision = 0;

	constructor(api: ProfileApi, auth: ProfileIdentity) {
		this.#api = api;
		this.#auth = auth;
	}

	/** Coalesce concurrent reads without caching completed responses or signed URLs. */
	getMyProfile(): Promise<UserProfile> {
		if (!this.#profileLoad) {
			const load = this.#loadProfile().finally(() => {
				if (this.#profileLoad === load) this.#profileLoad = undefined;
			});
			this.#profileLoad = load;
		}
		return this.#profileLoad;
	}

	async #loadProfile(): Promise<UserProfile> {
		const generation = this.#generation;
		const revision = this.#writeRevision;
		const profile = await this.#api.getMyProfile();
		this.#checkGeneration(generation);
		if (revision === this.#writeRevision) {
			this.profile = profile;
			return profile;
		}
		// A save landed while this read was in flight; its state is newer.
		return this.profile ?? profile;
	}

	/** Refreshes an expired signed avatar URL once across all consumers. */
	refreshPicture(): Promise<UserProfile | undefined> {
		if (!this.#pictureRefresh) {
			const refresh = this.getMyProfile()
				.catch(() => undefined)
				.finally(() => {
					if (this.#pictureRefresh === refresh) this.#pictureRefresh = undefined;
				});
			this.#pictureRefresh = refresh;
		}
		return this.#pictureRefresh;
	}

	async updateMyProfile(input: UpdateProfileInput): Promise<UserProfile> {
		const generation = this.#generation;
		const updated = await this.#api.updateMyProfile(input);
		this.#checkGeneration(generation);
		this.#writeRevision++;
		// Preserve picture fields from a concurrent picture update; this call
		// does not change them.
		this.profile = this.profile
			? {
					...this.profile,
					id: updated.id,
					email: updated.email,
					displayName: updated.displayName,
					description: updated.description,
					phoneNumber: updated.phoneNumber,
					role: updated.role
				}
			: updated;
		this.#auth.updateDisplayName(updated.displayName);
		return this.profile;
	}

	createProfilePictureUpload(
		input: { contentType: string; sizeBytes: number },
		signal: AbortSignal
	): Promise<ProfilePictureGrant> {
		return this.#api.createProfilePictureUpload(input, signal);
	}

	async updateMyProfilePicture(pictureId: string, signal: AbortSignal): Promise<UserProfile> {
		const generation = this.#generation;
		const updated = await this.#api.updateMyProfilePicture(pictureId, signal);
		this.#checkGeneration(generation);
		this.#writeRevision++;
		// Preserve editable fields from a concurrent profile save; only the
		// picture fields belong to this response.
		this.profile = this.profile
			? {
					...this.profile,
					profilePictureId: updated.profilePictureId,
					profilePictureUrl: updated.profilePictureUrl,
					profilePictureExpiresAt: updated.profilePictureExpiresAt
				}
			: updated;
		return this.profile;
	}

	async removeMyProfilePicture(signal: AbortSignal): Promise<UserProfile> {
		const generation = this.#generation;
		const updated = await this.#api.removeMyProfilePicture(signal);
		this.#checkGeneration(generation);
		this.#writeRevision++;
		this.profile = this.profile
			? {
					...this.profile,
					profilePictureId: updated.profilePictureId,
					profilePictureUrl: updated.profilePictureUrl,
					profilePictureExpiresAt: updated.profilePictureExpiresAt
				}
			: updated;
		return this.profile;
	}

	#checkGeneration(generation: number): void {
		if (generation !== this.#generation) {
			throw new DOMException('Profile identity changed', 'AbortError');
		}
	}

	clear(): void {
		this.#generation++;
		this.profile = null;
		this.#profileLoad = undefined;
		this.#pictureRefresh = undefined;
	}
}
