import type { PageInfo } from '$lib/gen/pagination/v1/pagination_pb';
import type { ReviewModerationApi, ReviewModerationRecord, LoadReviewsForUserParams } from './reviewModerationApi.ts';

// Rune-state wrapper around the review-moderation API. It owns the loading,
// error and cached record state for a reviewer's moderation page so a route can
// drive the UI from reactive getters without repeating try/catch.
export class ReviewModerationService {
	readonly #api: ReviewModerationApi;
	#records = $state<ReviewModerationRecord[]>([]);
	#pageInfo = $state<PageInfo | undefined>(undefined);
	#cmsAvailable = $state(true);
	#cmsError = $state<string | undefined>(undefined);
	#isLoading = $state(false);
	#hasLoaded = $state(false);
	#loadError = $state<unknown>(null);

	constructor(api: ReviewModerationApi) {
		this.#api = api;
	}

	get records(): ReviewModerationRecord[] {
		return this.#records;
	}

	get pageInfo(): PageInfo | undefined {
		return this.#pageInfo;
	}

	/** False after a load whose CMS job lookup degraded; the records stand. */
	get cmsAvailable(): boolean {
		return this.#cmsAvailable;
	}

	get cmsError(): string | undefined {
		return this.#cmsError;
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
			const page = await this.#api.loadReviewsForUser(params);
			this.#records = page.records;
			this.#pageInfo = page.pageInfo;
			this.#cmsAvailable = page.cmsAvailable;
			this.#cmsError = page.cmsError;
			this.#hasLoaded = true;
			return page.records;
		} catch (error) {
			this.#loadError = error;
			throw error;
		} finally {
			this.#isLoading = false;
		}
	}

	clear(): void {
		this.#records = [];
		this.#pageInfo = undefined;
		this.#cmsAvailable = true;
		this.#cmsError = undefined;
		this.#hasLoaded = false;
		this.#loadError = null;
	}
}
