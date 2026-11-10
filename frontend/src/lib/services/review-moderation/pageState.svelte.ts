import type { LoadReviewsForUserParams, ReviewModerationPage } from './reviewModerationApi.ts';
import type { ReviewModerationService } from './service.ts';

// Each mounted page owns an instance. Sensitive records never live in the shared service.
export class ReviewPageState {
	readonly #service: ReviewModerationService;
	readonly #currentAccount: () => string | undefined;
	#account: string | undefined;
	#reviewer: string | undefined;
	#request = 0;
	#page = $state<ReviewModerationPage | undefined>();
	#isLoading = $state(false);
	#loadError = $state<unknown>(null);

	constructor(service: ReviewModerationService, currentAccount: () => string | undefined) {
		this.#service = service;
		this.#currentAccount = currentAccount;
	}

	get records() {
		return this.#visible ? (this.#page?.records ?? []) : [];
	}
	get pageInfo() {
		return this.#visible ? this.#page?.pageInfo : undefined;
	}
	get cmsAvailable() {
		return this.#visible ? (this.#page?.cmsAvailable ?? true) : true;
	}
	get cmsError() {
		return this.#visible ? this.#page?.cmsError : undefined;
	}
	get isLoading() {
		return this.#visible && this.#isLoading;
	}
	get hasLoaded() {
		return this.#visible && this.#page !== undefined;
	}
	get loadError() {
		return this.#visible ? this.#loadError : null;
	}
	get #visible() {
		return this.#account === this.#currentAccount();
	}

	async loadReviews(params: LoadReviewsForUserParams): Promise<boolean> {
		const account = this.#currentAccount();
		if (this.#account !== account || this.#reviewer !== params.reviewerId) this.clear();
		this.#account = account;
		this.#reviewer = params.reviewerId;
		const request = ++this.#request;
		const current = () => request === this.#request && account === this.#currentAccount();
		this.#isLoading = true;
		this.#loadError = null;
		try {
			const page = await this.#service.loadReviews(params);
			if (!current()) return false;
			this.#page = page;
			return true;
		} catch (error) {
			if (!current()) return false;
			this.#loadError = error;
			throw error;
		} finally {
			if (current()) this.#isLoading = false;
		}
	}

	clear(): void {
		this.#request += 1;
		this.#page = undefined;
		this.#loadError = null;
		this.#isLoading = false;
		this.#account = undefined;
		this.#reviewer = undefined;
	}
}
