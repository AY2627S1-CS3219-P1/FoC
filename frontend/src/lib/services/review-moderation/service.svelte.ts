import type { ReviewModerationApi, ReviewModerationRecord, LoadReviewsForUserParams } from './reviewModerationApi.ts';

// Rune-state wrapper around the review-moderation API. It owns the loading,
// error and cached record state for a reviewer's moderation page so a route can
// drive the UI from reactive getters without repeating try/catch.
export class ReviewModerationService {
	readonly #api: ReviewModerationApi;
	#records = $state<ReviewModerationRecord[]>([]);
	#isLoading = $state(false);
	#hasLoaded = $state(false);
	#loadError = $state<unknown>(null);

	constructor(api: ReviewModerationApi) {
		this.#api = api;
	}

	get records(): ReviewModerationRecord[] {
		return this.#records;
	}

	get isLoading(): boolean {
		return this.#isLoading;
	}

	get hasLoaded(): boolean {
		return this.#hasLoaded;
	}

	get loadError(): unknown {
		return this.#loadError;
	}

	async loadReviews(params: LoadReviewsForUserParams): Promise<ReviewModerationRecord[]> {
		this.#isLoading = true;
		this.#loadError = null;
		try {
			const records = await this.#api.loadReviewsForUser(params);
			this.#records = records;
			this.#hasLoaded = true;
			return records;
		} catch (error) {
			this.#loadError = error;
			throw error;
		} finally {
			this.#isLoading = false;
		}
	}

	clear(): void {
		this.#records = [];
		this.#hasLoaded = false;
		this.#loadError = null;
	}
}
