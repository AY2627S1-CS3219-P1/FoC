import type {
	LoadReviewsForUserParams,
	ReviewModerationApi,
	ReviewModerationPage
} from './reviewModerationApi.ts';

export class ReviewModerationService {
	readonly #api: ReviewModerationApi;
	constructor(api: ReviewModerationApi) {
		this.#api = api;
	}
	loadReviews(params: LoadReviewsForUserParams): Promise<ReviewModerationPage> {
		return this.#api.loadReviewsForUser(params);
	}
}
